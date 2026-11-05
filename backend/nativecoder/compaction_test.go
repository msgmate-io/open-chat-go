package nativecoder

import (
	"strings"
	"testing"
)

// The compaction planner is deterministic, byte-capped, oldest-first and
// duplicate-dropping; these properties are the contract behind coder_compact
// (ci#394 gap 7, ticket ci#957).

func TestCoderCompactStoreBoundedAndOldestFirst(t *testing.T) {
	store := NewCoderCompactStore()
	if err := store.Compact("chat-1", "early material about the plan", 0); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if err := store.Compact("chat-1", "later material with details", 0); err != nil {
		t.Fatalf("compact: %v", err)
	}
	state := store.State("chat-1")
	if state.Rev != 2 {
		t.Fatalf("rev = %d, want 2", state.Rev)
	}
	if state.Capacity != contextSummaryCapacityBytes {
		t.Fatalf("capacity = %d, want default %d", state.Capacity, contextSummaryCapacityBytes)
	}
	if !strings.Contains(state.Summary, "early material") {
		t.Fatalf("summary must retain the oldest material first, got %q", state.Summary)
	}
	if !strings.Contains(state.Summary, "later material") {
		t.Fatalf("summary must fold in the new material, got %q", state.Summary)
	}
	if len(state.Summary) < 3 { // other runs may cap later; here the default cap is not reached
		t.Fatalf("summary unexpectedly tiny: %q", state.Summary)
	}
}

func TestCoderCompactStoreByteCapEvictsNewestFirst(t *testing.T) {
	store := NewCoderCompactStore()
	oldest := "oldest " + strings.Repeat("x", 88) // 95 bytes
	newest := "newest " + strings.Repeat("x", 200)
	if err := store.Compact("chat-1", oldest, 200); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if err := store.Compact("chat-1", newest, 200); err != nil {
		t.Fatalf("compact: %v", err)
	}
	state := store.State("chat-1")
	if len(state.Summary) > 200 {
		t.Fatalf("summary exceeds cap: %d bytes", len(state.Summary))
	}
	// The planner keeps the oldest material first and drops the newest tail
	// once the cap is hit, so the full oldest excerpt survives the fold...
	if !strings.Contains(state.Summary, oldest) {
		t.Fatalf("full oldest material must survive eviction, got %q", state.Summary)
	}
	// ...while the newest material is truncated (never fully present).
	if strings.Contains(state.Summary, newest) {
		t.Fatalf("material beyond the byte cap must be dropped, summary %q", state.Summary)
	}
}

func TestCoderCompactStoreDropsDuplicates(t *testing.T) {
	store := NewCoderCompactStore()
	text := "the same transcript excerpt repeated"
	for i := 0; i < 3; i++ {
		if err := store.Compact("chat-1", text, 0); err != nil {
			t.Fatalf("compact #%d: %v", i, err)
		}
	}
	state := store.State("chat-1")
	if state.Rev != 1 {
		t.Fatalf("duplicate folds must not bump the revision: rev = %d", state.Rev)
	}
	if strings.Count(state.Summary, "the same transcript") != 1 {
		t.Fatalf("duplicate material must be dropped, got %q", state.Summary)
	}
	// Whitespace-only folds are no-ops too.
	if err := store.Compact("chat-1", "   \n  ", 0); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if state2 := store.State("chat-1"); state2.Rev != 1 {
		t.Fatalf("whitespace fold must not bump the revision: rev = %d", state2.Rev)
	}
}

func TestCoderCompactStoreIsolationAndReset(t *testing.T) {
	store := NewCoderCompactStore()
	if err := store.Compact("chat-a", "alpha", 0); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if err := store.Compact("chat-b", "beta", 0); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if a := store.State("chat-a"); !strings.Contains(a.Summary, "alpha") || strings.Contains(a.Summary, "beta") {
		t.Fatalf("chats must not share summaries, got %q", a.Summary)
	}
	store.Reset("chat-a")
	if a := store.State("chat-a"); a.Summary != "" || a.Rev != 0 {
		t.Fatalf("reset must clear the summary, got %q rev %d", a.Summary, a.Rev)
	}
	if b := store.State("chat-b"); !strings.Contains(b.Summary, "beta") {
		t.Fatalf("resetting one chat must not clear another, got %q", b.Summary)
	}
}

func TestCoderCompactStateJSONStable(t *testing.T) {
	store := NewCoderCompactStore()
	if err := store.Compact("chat-1", "a bounded summary", 0); err != nil {
		t.Fatalf("compact: %v", err)
	}
	first, err := CoderCompactStateJSON(store.State("chat-1"))
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	second, err := CoderCompactStateJSON(store.State("chat-1"))
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if first != second {
		t.Fatalf("JSON must be stable across reads, got %q vs %q", first, second)
	}
	if !strings.Contains(first, `"summary"`) {
		t.Fatalf("JSON must carry the bounded summary, got %q", first)
	}
}

func TestCoderCompactRenderIsDeterministicText(t *testing.T) {
	store := NewCoderCompactStore()
	if err := store.Compact("chat-9", "first excerpt from the session", 1000); err != nil {
		t.Fatalf("compact: %v", err)
	}
	state := store.State("chat-9")
	rendered := RenderCoderCompactState(state)
	if !strings.Contains(rendered, "Context summary") {
		t.Fatalf("rendered must be deterministic text, got %q", rendered)
	}
	if !strings.Contains(rendered, "first excerpt") {
		t.Fatalf("rendered must carry the folded summary, got %q", rendered)
	}
	if !strings.Contains(rendered, "rev 1") {
		t.Fatalf("rendered must carry the revision, got %q", rendered)
	}
	// Rendering must not mutate the state: two renders agree.
	if again := RenderCoderCompactState(store.State("chat-9")); again != rendered {
		t.Fatalf("render must be deterministic, got %q vs %q", again, rendered)
	}
	// A chat with no state renders the empty-summary hint, not an error.
	empty := RenderCoderCompactState(store.State("no-such-chat"))
	if !strings.Contains(empty, "No session summary yet") {
		t.Fatalf("empty state must render the hint, got %q", empty)
	}
	// JSON must carry the bounded summary.
	js, err := CoderCompactStateJSON(store.State("chat-9"))
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.Contains(js, `"summary"`) {
		t.Fatalf("JSON must carry the summary, got %q", js)
	}
}
