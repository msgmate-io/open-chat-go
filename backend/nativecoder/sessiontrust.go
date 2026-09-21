package nativecoder

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// This file is the session-trust half of the opencode parity work (ci#394 gap
// "session_trust"): opencode can be told to allow a permission for the rest of
// the session, the native coder confirms every mutating action one at a time.
//
// The model here is deliberately *narrower* than "disable confirmations":
//
//   - trust is granted per *action class* (read/write/shell/git/network), never
//     per raw tool name, so a new mutating tool cannot inherit trust by accident,
//   - the class of a tool is derived (CoderTrustClassForTool) and an unknown tool
//     maps to no class at all, so it can never be trusted implicitly,
//   - trust is scoped to one chat's session (the store is keyed by chat id) and
//     revoked wholesale with RevokeAll/Clear when the session ends,
//   - read-only tools are not part of the trust decision at all
//     (CoderToolNeedsSessionTrust), so trusting "read" changes nothing.
//
// The store holds only the class names and a revision/timestamp of the last
// accepted change: no prompts, no command text, no secrets, tokens or user data.
// Granting trust is a decision of the confirmation layer, which asks the store
// (AllowsTool) before it asks the user again.

// CoderSessionTrustVersion is the shape version of the stored trust state. Bump
// it when the document shape changes.
const CoderSessionTrustVersion = 1

// Coder session trust actions, as requested by the `coder_trust` tool.
const (
	// CoderTrustActionGrant allows one or more classes for this session.
	CoderTrustActionGrant = "grant"
	// CoderTrustActionRevoke drops one or more classes (or all of them when the
	// request names no class).
	CoderTrustActionRevoke = "revoke"
	// CoderTrustActionStatus reports the current state without changing it.
	CoderTrustActionStatus = "status"
)

// Coder session trust action classes. They mirror the mutating families of the
// coder surface: writing the workspace, running a shell, running git, and
// reaching the network.
const (
	CoderTrustClassRead    = "read"
	CoderTrustClassWrite   = "write"
	CoderTrustClassShell   = "shell"
	CoderTrustClassGit     = "git"
	CoderTrustClassNetwork = "network"
	// CoderTrustClassAll is accepted on input and expands to every concrete
	// class; it is never stored as-is, so a class added later is not silently
	// covered by an old grant.
	CoderTrustClassAll = "all"
)

// Validation errors, exported so a caller can classify a rejected request
// instead of parsing the message.
var (
	ErrCoderTrustEmptyChat    = errors.New("coder session trust: chat id is required")
	ErrCoderTrustUnknownClass = errors.New("coder session trust: unknown action class")
	ErrCoderTrustEmptyClasses = errors.New("coder session trust: no action class given")
	// ErrCoderTrustUnknownAction is returned by the coder_trust tool surface when
	// an action is not grant, revoke or status.
	ErrCoderTrustUnknownAction = errors.New("coder session trust: unknown action")
)

// coderTrustToolClasses maps a coder tool name to the action class it belongs
// to. It is the single place a tool becomes trustable: a tool that is not listed
// here has no class, needs no trust and can never be granted any.
var coderTrustToolClasses = map[string]string{
	"coder_read":             CoderTrustClassRead,
	"coder_list":             CoderTrustClassRead,
	"coder_glob":             CoderTrustClassRead,
	"coder_grep":             CoderTrustClassRead,
	"coder_ask":              CoderTrustClassRead,
	"coder_select_workspace": CoderTrustClassRead,
	"coder_todo_read":        CoderTrustClassRead,
	"coder_write":            CoderTrustClassWrite,
	"coder_edit":             CoderTrustClassWrite,
	"coder_patch":            CoderTrustClassWrite,
	"coder_bash":             CoderTrustClassShell,
	"coder_git":              CoderTrustClassGit,
	"coder_webfetch":         CoderTrustClassNetwork,
	"coder_websearch":        CoderTrustClassNetwork,
}

// coderTrustConcreteClasses is the stored class order. Grants are normalized to
// it so two equal grants always render identically.
var coderTrustConcreteClasses = []string{
	CoderTrustClassRead,
	CoderTrustClassWrite,
	CoderTrustClassShell,
	CoderTrustClassGit,
	CoderTrustClassNetwork,
}

// CoderSessionTrustClasses returns the concrete action classes, in canonical
// order. `all` is not part of it on purpose (it is an input-only alias).
func CoderSessionTrustClasses() []string {
	out := make([]string, len(coderTrustConcreteClasses))
	copy(out, coderTrustConcreteClasses)
	return out
}

// CoderTrustClassIsValid reports whether a concrete class name is known. The
// `all` alias is accepted by NormalizeCoderTrustClasses but not by this
// predicate, so a caller can tell an alias from a stored class.
func CoderTrustClassIsValid(class string) bool {
	for _, known := range coderTrustConcreteClasses {
		if class == known {
			return true
		}
	}
	return false
}

// NormalizeCoderTrustClasses validates a requested class list and returns the
// canonical form: trimmed and lowercased names, `all` expanded to every concrete
// class, duplicates dropped and the result in canonical order. It never mutates
// the caller's slice. An empty or all-blank request is an error: "trust nothing"
// is expressed with revoke, never by an empty grant.
func NormalizeCoderTrustClasses(classes []string) ([]string, error) {
	expanded := map[string]bool{}
	for _, raw := range classes {
		class := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case class == "":
			continue
		case class == CoderTrustClassAll:
			for _, concrete := range coderTrustConcreteClasses {
				expanded[concrete] = true
			}
		case CoderTrustClassIsValid(class):
			expanded[class] = true
		default:
			return nil, fmt.Errorf("%w: %q", ErrCoderTrustUnknownClass, raw)
		}
	}
	if len(expanded) == 0 {
		return nil, ErrCoderTrustEmptyClasses
	}
	out := make([]string, 0, len(expanded))
	for _, class := range coderTrustConcreteClasses {
		if expanded[class] {
			out = append(out, class)
		}
	}
	return out, nil
}

// CoderTrustClassForTool returns the action class of a coder tool, or "" when
// the tool has no class (read-only helpers such as coder_todo_write, or a tool
// this build does not know). An empty class means "never trusted implicitly".
func CoderTrustClassForTool(tool string) string {
	return coderTrustToolClasses[strings.ToLower(strings.TrimSpace(tool))]
}

// CoderToolNeedsSessionTrust reports whether a tool is a mutating action that a
// session trust grant can cover. Read-only tools return false: trusting them
// would be a no-op, and a truthy answer here is what lets the confirmation layer
// skip its prompt.
func CoderToolNeedsSessionTrust(tool string) bool {
	class := CoderTrustClassForTool(tool)
	if class == "" {
		return false
	}
	return class != CoderTrustClassRead
}

// CoderSessionTrustState is the trust of one chat session plus its revision.
type CoderSessionTrustState struct {
	// Classes are the trusted action classes, in canonical order.
	Classes []string `json:"classes"`
	// Revision increments on every accepted change, so a consumer can render a
	// state once and skip an unchanged one.
	Revision int `json:"revision"`
	// UpdatedAt is the RFC3339 timestamp of the last accepted change.
	UpdatedAt string `json:"updated_at,omitempty"`
	// Version is the shape version of the stored state.
	Version int `json:"version,omitempty"`
}

// Empty reports whether nothing is trusted for the session.
func (s CoderSessionTrustState) Empty() bool {
	return len(s.Classes) == 0
}

// Allows reports whether a concrete action class is trusted.
func (s CoderSessionTrustState) Allows(class string) bool {
	class = strings.ToLower(strings.TrimSpace(class))
	for _, trusted := range s.Classes {
		if trusted == class {
			return true
		}
	}
	return false
}

// AllowsTool reports whether a tool call may skip its per-action confirmation
// because the session trusts its class. A tool without a class is never allowed.
func (s CoderSessionTrustState) AllowsTool(tool string) bool {
	class := CoderTrustClassForTool(tool)
	if class == "" {
		return false
	}
	return s.Allows(class)
}

// Metadata renders the trust state as JSON-friendly state (revision, trusted
// classes, whether a tool class is covered) instead of only as rendered text.
func (s CoderSessionTrustState) Metadata() map[string]interface{} {
	classes := s.Classes
	if classes == nil {
		classes = []string{}
	}
	meta := map[string]interface{}{
		"version":  CoderSessionTrustVersion,
		"revision": s.Revision,
		"classes":  classes,
		"trusted":  len(classes) > 0,
	}
	for _, class := range coderTrustConcreteClasses {
		meta[class] = s.Allows(class)
	}
	if s.UpdatedAt != "" {
		meta["updated_at"] = s.UpdatedAt
	}
	return meta
}

// CoderSessionTrustStore holds the trust of every chat session of one coder
// runtime. It is safe for concurrent use: a grant is written by a tool call and
// read by the confirmation layer while the turn keeps running.
type CoderSessionTrustStore struct {
	mu     sync.Mutex
	states map[string]CoderSessionTrustState
}

// NewCoderSessionTrustStore returns an empty store (nothing is trusted).
func NewCoderSessionTrustStore() *CoderSessionTrustStore {
	return &CoderSessionTrustStore{states: map[string]CoderSessionTrustState{}}
}

// Grant allows the requested classes for chatID and returns the new state. It is
// additive: granting a class twice keeps one entry and still bumps the revision,
// so the confirmation layer always observes the change.
func (s *CoderSessionTrustStore) Grant(chatID string, classes []string, at time.Time) (CoderSessionTrustState, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return CoderSessionTrustState{}, ErrCoderTrustEmptyChat
	}
	normalized, err := NormalizeCoderTrustClasses(classes)
	if err != nil {
		return CoderSessionTrustState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.states[chatID]
	merged := map[string]bool{}
	for _, class := range current.Classes {
		merged[class] = true
	}
	for _, class := range normalized {
		merged[class] = true
	}
	next := CoderSessionTrustState{
		Classes:   canonicalTrustClasses(merged),
		Revision:  current.Revision + 1,
		UpdatedAt: at.UTC().Format(time.RFC3339),
		Version:   CoderSessionTrustVersion,
	}
	s.states[chatID] = next
	return next, nil
}

// Revoke drops the requested classes for chatID and returns the new state. An
// empty (or all-blank) class list revokes everything, which is how the session
// trust is reset.
func (s *CoderSessionTrustStore) Revoke(chatID string, classes []string, at time.Time) (CoderSessionTrustState, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return CoderSessionTrustState{}, ErrCoderTrustEmptyChat
	}
	drop := map[string]bool{}
	wanted := false
	for _, raw := range classes {
		class := strings.ToLower(strings.TrimSpace(raw))
		if class == "" {
			continue
		}
		wanted = true
		if class == CoderTrustClassAll {
			for _, concrete := range coderTrustConcreteClasses {
				drop[concrete] = true
			}
			continue
		}
		if !CoderTrustClassIsValid(class) {
			return CoderSessionTrustState{}, fmt.Errorf("%w: %q", ErrCoderTrustUnknownClass, raw)
		}
		drop[class] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.states[chatID]
	if !wanted {
		return s.resetLocked(chatID, current, at), nil
	}
	remaining := map[string]bool{}
	for _, class := range current.Classes {
		if !drop[class] {
			remaining[class] = true
		}
	}
	if len(remaining) == 0 {
		return s.resetLocked(chatID, current, at), nil
	}
	next := CoderSessionTrustState{
		Classes:   canonicalTrustClasses(remaining),
		Revision:  current.Revision + 1,
		UpdatedAt: at.UTC().Format(time.RFC3339),
		Version:   CoderSessionTrustVersion,
	}
	s.states[chatID] = next
	return next, nil
}

// resetLocked stores an empty state (the session trusts nothing) and reports it.
// The revision keeps counting up so a consumer that observed a trusted state
// still sees a change when the trust is dropped.
func (s *CoderSessionTrustStore) resetLocked(chatID string, current CoderSessionTrustState, at time.Time) CoderSessionTrustState {
	next := CoderSessionTrustState{
		Classes:   []string{},
		Revision:  current.Revision + 1,
		UpdatedAt: at.UTC().Format(time.RFC3339),
		Version:   CoderSessionTrustVersion,
	}
	s.states[chatID] = next
	return next
}

// RevokeAll forgets every class of chatID and returns the resulting state.
func (s *CoderSessionTrustStore) RevokeAll(chatID string, at time.Time) CoderSessionTrustState {
	state, err := s.Revoke(chatID, nil, at)
	if err != nil {
		return CoderSessionTrustState{Classes: []string{}, Version: CoderSessionTrustVersion}
	}
	return state
}

// State returns the trust of chatID (an empty state when the chat has none).
func (s *CoderSessionTrustStore) State(chatID string) CoderSessionTrustState {
	chatID = strings.TrimSpace(chatID)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[chatID]
}

// Allows reports whether a concrete class is trusted for chatID.
func (s *CoderSessionTrustStore) Allows(chatID string, class string) bool {
	return s.State(chatID).Allows(class)
}

// AllowsTool reports whether a tool call may skip its confirmation for chatID.
func (s *CoderSessionTrustStore) AllowsTool(chatID string, tool string) bool {
	return s.State(chatID).AllowsTool(tool)
}

// Clear forgets the trust of chatID (used when a session ends).
func (s *CoderSessionTrustStore) Clear(chatID string) {
	chatID = strings.TrimSpace(chatID)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, chatID)
}

// canonicalTrustClasses renders a class set in canonical order.
func canonicalTrustClasses(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, class := range coderTrustConcreteClasses {
		if set[class] {
			out = append(out, class)
		}
	}
	return out
}

// RenderCoderSessionTrust renders the trust state as one human-readable line for
// the tool result and the chat transcript.
func RenderCoderSessionTrust(state CoderSessionTrustState) string {
	if state.Empty() {
		return "Session trust: nothing is trusted, so every mutating action is confirmed individually."
	}
	classes := make([]string, len(state.Classes))
	copy(classes, state.Classes)
	sort.Strings(classes)
	return fmt.Sprintf("Session trust (revision %d): %s - mutating actions in these classes run without a per-action confirmation until the session ends or the trust is revoked.",
		state.Revision, strings.Join(classes, ", "))
}

// CoderSessionTrustJSON marshals the state for a structured tool result.
func CoderSessionTrustJSON(state CoderSessionTrustState) (string, error) {
	payload := struct {
		Version   int      `json:"version"`
		Classes   []string `json:"classes"`
		Revision  int      `json:"revision"`
		UpdatedAt string   `json:"updated_at,omitempty"`
	}{
		Version:   CoderSessionTrustVersion,
		Classes:   state.Classes,
		Revision:  state.Revision,
		UpdatedAt: state.UpdatedAt,
	}
	if payload.Classes == nil {
		payload.Classes = []string{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
