package nativecoder

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompileCoderSummaryCombinesNotesAndDedupes verifies the happy path of the
// compaction planner (ci#394 gap 7): persisted findings are kept, session notes
// are appended, an exact repeat is dropped, and the result is framed and JSON.
func TestCompileCoderSummaryCombinesNotesAndDedupes(t *testing.T) {
	existing := "Identified root cause A"
	notes := []string{"Fix the retry loop", "Run targeted tests", "Fix the retry loop"}
	res, err := CompileCoderSummary(existing, notes, 0)
	if err != nil {
		t.Fatalf("CompileCoderSummary: %v", err)
	}
	if res.Meta.LinesAdded != 2 {
		t.Errorf("LinesAdded=%d, want 2", res.Meta.LinesAdded)
	}
	if res.Meta.DupesDropped != 1 {
		t.Errorf("DupesDropped=%d, want 1", res.Meta.DupesDropped)
	}
	if res.Meta.LinesKept != 3 {
		t.Errorf("LinesKept=%d, want 3 (existing + two distinct notes)", res.Meta.LinesKept)
	}
	if !res.Meta.AlreadyStable {
		t.Errorf("Existing was given, AlreadyStable should be true")
	}
	if !res.Meta.Persist {
		t.Errorf("expected Persist with new notes to record")
	}
	for _, want := range []string{"Identified root cause A", "Fix the retry loop", "Run targeted tests"} {
		if !strings.Contains(res.Summary, want) {
			t.Errorf("summary missing %q:\n%s", want, res.Summary)
		}
	}
	if res.Rendered != res.Summary {
		t.Errorf("Rendered must equal Summary, got differing content")
	}
	if res.JSON == "" {
		t.Fatalf("expected JSON meta")
	}
	var rt CoderCompactionMeta
	if err := json.Unmarshal([]byte(res.JSON), &rt); err != nil {
		t.Fatalf("JSON meta invalid: %v", err)
	}
	if rt.LinesAdded != res.Meta.LinesAdded {
		t.Errorf("JSON LinesAdded=%d, want %d", rt.LinesAdded, res.Meta.LinesAdded)
	}
	if rt.Bytes != res.Meta.Bytes {
		t.Errorf("JSON Bytes=%d, want %d", rt.Bytes, res.Meta.Bytes)
	}
}

// TestCompileCoderSummaryTrimsOldestToCap verifies that an overlong session is
// bounded by dropping the oldest finding lines first, keeping the newest notes.
func TestCompileCoderSummaryTrimsOldestToCap(t *testing.T) {
	var existing []string
	for i := 0; i < 50; i++ {
		existing = append(existing, "old finding number "+string(rune('A'+i%26)))
	}
	notes := []string{"the newest note for the next turn"}
	res, err := CompileCoderSummary(strings.Join(existing, "\n"), notes, 96)
	if err != nil {
		t.Fatalf("CompileCoderSummary: %v", err)
	}
	if res.Meta.Truncated == 0 {
		t.Errorf("expected truncation when the session exceeds the byte cap")
	}
	if res.Meta.LinesKept <= 0 {
		t.Errorf("expected some findings to survive, got LinesKept=%d", res.Meta.LinesKept)
	}
	if !strings.Contains(res.Summary, "the newest note for the next turn") {
		t.Errorf("newest note must survive the cap:\n%s", res.Summary)
	}
	if strings.Contains(res.Summary, "old finding number A") {
		t.Errorf("oldest finding should be dropped by the cap")
	}
	if res.Meta.Bytes > res.Meta.MaxBytes+len("Compacted session summary (ci#394):\n")+4 {
		t.Errorf("summary exceeded bound: bytes=%d max=%d", res.Meta.Bytes, res.Meta.MaxBytes)
	}
}

// TestCompileCoderSummarySkipsPersistWhenNothingNew verifies that re-compacting
// an already-recorded summary with no new notes reports Persist=false so the
// integration does not rewrite unchanged state.
func TestCompileCoderSummarySkipsPersistWhenNothingNew(t *testing.T) {
	res, err := CompileCoderSummary("The only persisted finding", nil, 0)
	if err != nil {
		t.Fatalf("CompileCoderSummary: %v", err)
	}
	if !res.Meta.AlreadyStable {
		t.Errorf("Existing present, AlreadyStable should be true")
	}
	if res.Meta.Persist {
		t.Errorf("no new notes and existing present: Persist should be false")
	}
	if !strings.Contains(res.Summary, "The only persisted finding") {
		t.Errorf("persisted finding must be kept:\n%s", res.Summary)
	}
}

// TestCompileCoderSummaryEmptyInput produces a stable, non-persisting empty
// result rather than panicking on a fresh session.
func TestCompileCoderSummaryEmptyInput(t *testing.T) {
	res, err := CompileCoderSummary("", nil, 0)
	if err != nil {
		t.Fatalf("CompileCoderSummary: %v", err)
	}
	if res.Meta.AlreadyStable {
		t.Errorf("no existing summary, AlreadyStable should be false")
	}
	if res.Meta.Persist {
		t.Errorf("no findings at all, Persist should be false")
	}
	if !strings.Contains(res.Summary, "Compacted session summary") {
		t.Errorf("expected the compaction frame:\n%s", res.Summary)
	}
}
