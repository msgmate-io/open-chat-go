package nativecoder

// This file is the core-side state machine for the context compaction gap
// (ci#394 gap 7, ticket ci#957): a bounded, deterministic, per-chat
// summarizer. A long coder interaction can exhaust the provider context
// window (the context_window_exceeded classification); coder_context reports
// the budget but nothing produced a persisted summary to survive a long
// session. This store gives the agent a compact surface (coder_compact) whose
// output is byte-capped, keeps the oldest material first (newer material is
// dropped rather than evicting what is already summarized), and never grows
// past the cap - so the bounded context_summary the reader injects (ci#793)
// stays within the window.
//
// The planner is deliberately pure and deterministic:
//
//   - Keep is the current bounded summary (oldest material first).
//   - Fold appends new material and re-caps.
//   - Drop removes material with a duplicate fingerprint (same text) instead
//     of keeping both copies, so repeated tool output cannot bloat the
//     summary.
//
// Every method is synchronous and free of side effects beyond the store, so
// the whole planner is unit-testable without an LLM runtime.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// contextSummaryCapacityBytes is the default byte cap for a chat's summary
// (used when a compact call carries no explicit capacity). It is small on
// purpose: the summary is re-injected into the provider window on every read,
// so it must stay within a bounded fraction of the window.
const contextSummaryCapacityBytes = 12_000

// CoderCompactionFingerprintFunc hashes one piece of material so the planner
// can drop duplicates deterministically. It is a package-level var so a test
// can force collisions; the zero value is a SHA-256 of the input.
var CoderCompactionFingerprintFunc = func(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// CoderCompactStore holds one bounded summary per chat. The store is safe for
// concurrent use.
type CoderCompactStore struct {
	mu      sync.Mutex
	states  map[string]*coderCompactState
	version int64
}

// coderCompactState is the persisted per-chat compaction state.
type coderCompactState struct {
	// Summary is the bounded, oldest-first markdown summary of the session.
	Summary string
	// Fingerprints are the hashes of the material already folded in, so a
	// repeat can be dropped instead of duplicated.
	Fingerprints map[string]struct{}
	// Rev is the compaction revision, bumped on every persisted change.
	Rev int
	// UpdatedAt is the instant of the last persisted change.
	UpdatedAt time.Time
	// Capacity is the byte cap in effect for this chat.
	Capacity int
}

// NewCoderCompactStore returns an empty compaction store.
func NewCoderCompactStore() *CoderCompactStore {
	return &CoderCompactStore{states: make(map[string]*coderCompactState)}
}

// Compact folds Text into the per-chat summary under capBytes (the package
// default when capBytes is zero) and persists the change. A blank Text that
// duplicates already-folded material is a no-op. The persistence is
// transactional: on error nothing changes.
func (s *CoderCompactStore) Compact(chatID, text string, capBytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.state(chatID)
	if capBytes <= 0 {
		capBytes = contextSummaryCapacityBytes
	}
	state.Capacity = capBytes

	if text == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	fp := CoderCompactionFingerprintFunc(strings.TrimSpace(text))
	if _, dup := state.Fingerprints[fp]; dup {
		return nil
	}

	// Oldest material first: what we already have is older than the new
	// text, so the new text is appended, then the whole summary is re-capped
	// (oldest parts evicted first, newest dropped once the cap is reached).
	state.Summary = capSummary(state.Summary+"\n\n"+text, capBytes)
	state.Fingerprints[fp] = struct{}{}
	state.Rev++
	state.UpdatedAt = time.Now()
	return nil
}

// State returns a snapshot of a chat's compaction state (a chat without
// state reads an empty summary at rev 0).
func (s *CoderCompactStore) State(chatID string) CoderCompactStateView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state(chatID).view()
}

// Reset clears a chat's compaction state.
func (s *CoderCompactStore) Reset(chatID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, chatID)
}

func (s *CoderCompactStore) state(chatID string) *coderCompactState {
	st, ok := s.states[chatID]
	if !ok {
		st = &coderCompactState{Fingerprints: make(map[string]struct{})}
		s.states[chatID] = st
	}
	return st
}

func (c *coderCompactState) view() CoderCompactStateView {
	summary := c.Summary
	if summary == "" {
		summary = ""
	}
	return CoderCompactStateView{
		ChatID:    "",
		Summary:   summary,
		Rev:       c.Rev,
		UpdatedAt: c.UpdatedAt,
		Capacity:  c.Capacity,
	}
}

// CoderCompactStateView is the public snapshot of a chat's compaction state.
type CoderCompactStateView struct {
	// ChatID identifies the chat the summary belongs to.
	ChatID string
	// Summary is the bounded, oldest-first summary of the session.
	Summary string
	// Rev is the compaction revision of this view.
	Rev int
	// UpdatedAt is the instant of the last persisted change.
	UpdatedAt time.Time
	// Capacity is the byte cap the summary is bounded by.
	Capacity int
}

// ErrCoderCompactionTooLarge is returned when even the oldest material alone
// exceeds the cap, so no bounded summary can be produced.
var ErrCoderCompactionTooLarge = errors.New("nativecoder: material exceeds the compaction byte cap entirely")

// capSummary truncates text to at most cap bytes on a UTF-8 rune boundary,
// keeping the head. It is the single place the bounded invariant is enforced,
// so "bounded" is testable by construction.
func capSummary(text string, cap int) string {
	if cap <= 0 {
		return ""
	}
	if len(text) <= cap {
		return text
	}
	cut := text[:cap]
	for cut != "" && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// Fold folds material into an existing summary deterministically: it drops
// material whose fingerprint already appears in the summary (duplicate-drop),
// appends what is new, and caps the result. Used by the render path so a
// partial summary never leaves the planner unbounded.
//
// Deprecated: kept for the pure-planner tests; callers should use the store's
// Compact method.
func Fold(summary, text string, capacity int) string {
	if capacity <= 0 {
		capacity = contextSummaryCapacityBytes
	}
	if text == "" || strings.TrimSpace(text) == "" {
		return capSummary(summary, capacity)
	}
	candidate := summary
	if !strings.Contains(candidate, text) {
		candidate += "\n\n" + text
	}
	return capSummary(candidate, capacity)
}

// RenderCoderCompactState renders the bounded summary as deterministic text.
func RenderCoderCompactState(state CoderCompactStateView) string {
	if state.Summary == "" {
		return "No session summary yet; call coder_compact with a transcript excerpt to persist a bounded summary."
	}
	return "Context summary (rev " + itoa(state.Rev) + ", " + itoa(len(state.Summary)) + " bytes):\n" + state.Summary
}

// CoderCompactStateJSON is the stable JSON form of the compaction state; it
// never includes message text beyond the summary itself.
func CoderCompactStateJSON(state CoderCompactStateView) (string, error) {
	buf, err := marshalCompactionJSON(state)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func marshalCompactionJSON(state CoderCompactStateView) ([]byte, error) {
	type view struct {
		ChatID    string `json:"chat_id"`
		Summary   string `json:"summary"`
		Rev       int    `json:"rev"`
		UpdatedAt string `json:"updated_at,omitempty"`
		Capacity  int    `json:"capacity"`
	}
	v := view{
		ChatID:   state.ChatID,
		Summary:  state.Summary,
		Rev:      state.Rev,
		Capacity: state.Capacity,
	}
	if !state.UpdatedAt.IsZero() {
		v.UpdatedAt = state.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return json.Marshal(v)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
