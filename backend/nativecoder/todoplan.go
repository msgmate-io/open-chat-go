package nativecoder

// Per-chat todo plan — opencode `todowrite`/`todoread` parity (ci#394 gap 1).
//
// The native coder surface (`coder_read`, `coder_edit`, `coder_bash`, ...) let
// the agent act, but a long task had no structured progress state: plan steps
// lived only inside the model's context, so a compaction, a turn boundary or a
// re-attach could drop them and the visible chat never showed what was done and
// what was left. opencode solves this with a `todowrite`/`todoread` pair backed
// by a per-session list.
//
// This file provides the core-side state for the same two operations:
//
//   - `Write` replaces the plan of one chat (what `coder_todo_write` will call),
//     validating it and bumping a revision so a consumer can tell a rewrite
//     apart from the plan it already rendered;
//   - `Read` returns the plan of one chat (what `coder_todo_read` will call) as
//     structured state, so the agent can re-read its own plan after compaction;
//   - `Render` turns a plan into the deterministic markdown the chat shows.
//
// The store is in-memory and per-chat: a plan is scratch state for the running
// interaction, not durable configuration, so it deliberately does not touch the
// chat's shared config. Statuses and the "at most one step in progress" rule
// mirror the opencode contract so the two integrations behave the same way.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// CoderTodoWriteToolName / CoderTodoReadToolName are the coder tool names
	// the todo plan is exposed under (opencode's `todowrite`/`todoread`).
	CoderTodoWriteToolName = "coder_todo_write"
	CoderTodoReadToolName  = "coder_todo_read"

	// CoderTodoParityGapID names the ci#394 parity gap this state closes.
	CoderTodoParityGapID = "todo_list"

	// CoderTodoPlanVersion versions the shape of a stored plan.
	CoderTodoPlanVersion = 1

	// MaxCoderTodoItems caps a single plan. A plan is a summary of intent, not a
	// transcript: the cap keeps a runaway rewrite from pinning unbounded state
	// and forces the agent to keep the list readable.
	MaxCoderTodoItems = 64
)

// Item statuses, matching the opencode contract.
const (
	CoderTodoStatusPending    = "pending"
	CoderTodoStatusInProgress = "in_progress"
	CoderTodoStatusCompleted  = "completed"
	CoderTodoStatusCancelled  = "cancelled"
)

// Validation errors, exported so a caller can classify a rejected write instead
// of parsing the message.
var (
	ErrCoderTodoEmptyChat       = errors.New("coder todo: chat id is required")
	ErrCoderTodoTooManyItems    = errors.New("coder todo: too many items")
	ErrCoderTodoEmptyContent    = errors.New("coder todo: item content is required")
	ErrCoderTodoUnknownStatus   = errors.New("coder todo: unknown item status")
	ErrCoderTodoManyInProgress  = errors.New("coder todo: at most one item may be in progress")
	ErrCoderTodoDuplicateItemID = errors.New("coder todo: duplicate item id")
)

// CoderTodoItem is one planned step.
type CoderTodoItem struct {
	// ID identifies the step within its plan. A caller may leave it empty; the
	// store then assigns a stable positional id (`1`, `2`, ...) so a rewrite
	// that only edits content keeps addressing the same rows.
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

// CoderTodoPlan is the plan of one chat plus its revision.
type CoderTodoPlan struct {
	Items []CoderTodoItem `json:"items"`
	// Revision increments on every accepted write, so a consumer can render a
	// plan once and skip an unchanged one.
	Revision int `json:"revision"`
	// UpdatedAt is the RFC3339 timestamp of the last accepted write.
	UpdatedAt string `json:"updated_at,omitempty"`
	// Version is the shape version of the stored plan.
	Version int `json:"version,omitempty"`
}

// CoderTodoSummary counts the plan by status.
type CoderTodoSummary struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Cancelled  int `json:"cancelled"`
	Total      int `json:"total"`
}

// CoderTodoStatusIsValid reports whether a status belongs to the contract. The
// empty string is accepted by NormalizeCoderTodoItems (it defaults to pending)
// but not by this predicate, so callers can tell "unset" from "set".
func CoderTodoStatusIsValid(status string) bool {
	switch status {
	case CoderTodoStatusPending, CoderTodoStatusInProgress, CoderTodoStatusCompleted, CoderTodoStatusCancelled:
		return true
	default:
		return false
	}
}

// NormalizeCoderTodoItems validates a submitted plan and returns the canonical
// form: trimmed content, positional ids for unset ids, a status for every row
// and at most one row in progress. It never mutates the caller's slice.
func NormalizeCoderTodoItems(items []CoderTodoItem) ([]CoderTodoItem, error) {
	if len(items) > MaxCoderTodoItems {
		return nil, fmt.Errorf("%w: %d > %d", ErrCoderTodoTooManyItems, len(items), MaxCoderTodoItems)
	}
	normalized := make([]CoderTodoItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	inProgress := 0
	for index, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return nil, fmt.Errorf("%w (item %d)", ErrCoderTodoEmptyContent, index+1)
		}
		status := strings.TrimSpace(item.Status)
		if status == "" {
			status = CoderTodoStatusPending
		}
		if !CoderTodoStatusIsValid(status) {
			return nil, fmt.Errorf("%w: %q", ErrCoderTodoUnknownStatus, status)
		}
		if status == CoderTodoStatusInProgress {
			inProgress++
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("%d", index+1)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%w: %q", ErrCoderTodoDuplicateItemID, id)
		}
		seen[id] = struct{}{}
		normalized = append(normalized, CoderTodoItem{
			ID:       id,
			Content:  content,
			Status:   status,
			Priority: strings.TrimSpace(item.Priority),
		})
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("%w: %d", ErrCoderTodoManyInProgress, inProgress)
	}
	return normalized, nil
}

// CoderTodoSummary counts a plan by status.
func (p CoderTodoPlan) Summary() CoderTodoSummary {
	summary := CoderTodoSummary{Total: len(p.Items)}
	for _, item := range p.Items {
		switch item.Status {
		case CoderTodoStatusInProgress:
			summary.InProgress++
		case CoderTodoStatusCompleted:
			summary.Completed++
		case CoderTodoStatusCancelled:
			summary.Cancelled++
		default:
			summary.Pending++
		}
	}
	return summary
}

// NextItem returns the first step that still needs work: the item in progress,
// else the first pending one. ok is false when the plan is finished or empty, so
// a caller can tell "nothing left to do" from "no plan".
func (p CoderTodoPlan) NextItem() (CoderTodoItem, bool) {
	for _, item := range p.Items {
		if item.Status == CoderTodoStatusInProgress {
			return item, true
		}
	}
	for _, item := range p.Items {
		if item.Status == CoderTodoStatusPending {
			return item, true
		}
	}
	return CoderTodoItem{}, false
}

// Metadata renders the plan as JSON-friendly state, so the todo list is
// inspectable as structured tool metadata (revision + per-status counts) rather
// than only as rendered text.
func (p CoderTodoPlan) Metadata() map[string]interface{} {
	summary := p.Summary()
	meta := map[string]interface{}{
		"version":     CoderTodoPlanVersion,
		"revision":    p.Revision,
		"item_count":  summary.Total,
		"pending":     summary.Pending,
		"in_progress": summary.InProgress,
		"completed":   summary.Completed,
		"cancelled":   summary.Cancelled,
	}
	if p.UpdatedAt != "" {
		meta["updated_at"] = p.UpdatedAt
	}
	if next, ok := p.NextItem(); ok {
		meta["next_item_id"] = next.ID
		meta["next_item"] = next.Content
	}
	return meta
}

// CoderTodoStore holds the plan of every chat of one coder runtime. It is safe
// for concurrent use: a plan is written by a tool call and read by the tool loop
// and the chat renderer.
type CoderTodoStore struct {
	mu    sync.Mutex
	plans map[string]CoderTodoPlan
}

// NewCoderTodoStore returns an empty store.
func NewCoderTodoStore() *CoderTodoStore {
	return &CoderTodoStore{plans: map[string]CoderTodoPlan{}}
}

// Write replaces the plan of chatID (opencode `todowrite`). An empty item list
// clears the plan without error: dropping finished steps after the work landed
// must not need a separate call. The returned plan is the stored one, including
// its new revision.
func (s *CoderTodoStore) Write(chatID string, items []CoderTodoItem, at time.Time) (CoderTodoPlan, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return CoderTodoPlan{}, ErrCoderTodoEmptyChat
	}
	normalized, err := NormalizeCoderTodoItems(items)
	if err != nil {
		return CoderTodoPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.plans == nil {
		s.plans = map[string]CoderTodoPlan{}
	}
	previous := s.plans[chatID]
	plan := CoderTodoPlan{
		Items:    normalized,
		Revision: previous.Revision + 1,
		Version:  CoderTodoPlanVersion,
	}
	if !at.IsZero() {
		plan.UpdatedAt = at.UTC().Format(time.RFC3339)
	} else {
		plan.UpdatedAt = previous.UpdatedAt
	}
	s.plans[chatID] = plan
	return plan, nil
}

// Read returns the plan of chatID (opencode `todoread`). A chat without a plan
// returns an empty, non-nil plan with revision 0.
func (s *CoderTodoStore) Read(chatID string) CoderTodoPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[strings.TrimSpace(chatID)]
	if !ok {
		return CoderTodoPlan{Items: []CoderTodoItem{}, Version: CoderTodoPlanVersion}
	}
	items := make([]CoderTodoItem, len(plan.Items))
	copy(items, plan.Items)
	plan.Items = items
	return plan
}

// Clear drops the plan of chatID, so a finished interaction does not leave the
// chat's scratch state behind.
func (s *CoderTodoStore) Clear(chatID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.plans, strings.TrimSpace(chatID))
}

// RenderCoderTodoPlan renders a plan as the deterministic markdown a chat shows:
// in-progress steps first, then pending, completed and cancelled, each preserving
// its submitted order. The rendering is stable for a given plan so a consumer can
// diff it, and an empty plan renders as a fixed sentence rather than a blank box.
func RenderCoderTodoPlan(plan CoderTodoPlan) string {
	if len(plan.Items) == 0 {
		if plan.Revision == 0 {
			return "Todo plan: none."
		}
		return fmt.Sprintf("Todo plan (revision %d): none.", plan.Revision)
	}
	summary := plan.Summary()
	var builder strings.Builder
	fmt.Fprintf(&builder, "Todo plan (revision %d): %d in progress, %d pending, %d completed, %d cancelled.\n",
		plan.Revision, summary.InProgress, summary.Pending, summary.Completed, summary.Cancelled)
	for _, status := range []string{
		CoderTodoStatusInProgress,
		CoderTodoStatusPending,
		CoderTodoStatusCompleted,
		CoderTodoStatusCancelled,
	} {
		for _, item := range plan.Items {
			if item.Status != status {
				continue
			}
			builder.WriteString(renderCoderTodoLine(item))
			builder.WriteString("\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// CoderTodoPlanJSON marshals a plan into the stable JSON form a structured tool
// result carries (revision, status counts, version). It never includes command
// or output text, so serialized plan metadata stays safe to render anywhere.
func CoderTodoPlanJSON(plan CoderTodoPlan) (string, error) {
	payload := struct {
		Revision int              `json:"revision"`
		Version  int              `json:"version,omitempty"`
		Summary  CoderTodoSummary `json:"summary"`
	}{
		Revision: plan.Revision,
		Version:  plan.Version,
		Summary:  plan.Summary(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func renderCoderTodoLine(item CoderTodoItem) string {
	priority := ""
	if item.Priority != "" {
		priority = fmt.Sprintf(" (%s)", item.Priority)
	}
	switch item.Status {
	case CoderTodoStatusCompleted:
		return fmt.Sprintf("- [x] %s%s", item.Content, priority)
	case CoderTodoStatusCancelled:
		return fmt.Sprintf("- [x] ~~%s~~ (cancelled)%s", item.Content, priority)
	case CoderTodoStatusInProgress:
		return fmt.Sprintf("- [ ] %s (in progress)%s", item.Content, priority)
	default:
		return fmt.Sprintf("- [ ] %s%s", item.Content, priority)
	}
}
