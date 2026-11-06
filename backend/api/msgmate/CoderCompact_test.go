package msgmate

import (
	"strings"
	"testing"

	"github.com/msgmate-io/open-chat-go-community-coder-integration/coder"
)

// ci#394 gap 7 (ticket ci#957): the coder_compact runner folds a transcript
// excerpt into the per-chat bounded summary and answers read calls with the
// current state. It resolves the process-wide coder parity surface lazily, so a
// test can point the surface at a scratch store and observe oldest-first,
// duplicate-dropped behaviour end to end.

// scratchCoderCompactSurface returns a runner bound to a fresh parity surface.
// The surface seam is swapped for the duration of the test and restored
// afterwards via coderCompactResetTest.
func scratchCoderCompactRunner(t *testing.T) *coderCompactRunner {
	t.Helper()
	original := coderCompactGlobalSurface
	coderCompactGlobalSurface = func() *coder.CoderParitySurface {
		return coder.NewCoderParitySurface("")
	}
	t.Cleanup(func() {
		coderCompactGlobalSurface = original
		coderCompactResetTest()
	})
	return &coderCompactRunner{}
}

func TestCoderCompactRequiresChat(t *testing.T) {
	runner := scratchCoderCompactRunner(t)
	if _, err := runner.CompactCoderContext("", "some text", 0); err == nil {
		t.Fatal("a missing chat uuid must be refused")
	}
	if _, err := runner.CompactCoderContext("   ", "some text", 0); err == nil {
		t.Fatal("a blank chat uuid must be refused")
	}
}

func TestCoderCompactFoldsAndReadsBack(t *testing.T) {
	runner := scratchCoderCompactRunner(t)

	state, err := runner.CompactCoderContext("chat-compact-1", "first decision", 0)
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if !strings.Contains(anyString(state["summary"]), "first decision") {
		t.Fatalf("summary did not contain the folded material: %v", state)
	}

	// Reading back with no fold returns the same material.
	readBack, err := runner.CoderContextSummary("chat-compact-1")
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	if !strings.Contains(anyString(readBack["summary"]), "first decision") {
		t.Fatalf("read-back summary lost the folded material: %v", readBack)
	}

	// A different chat has its own empty summary.
	otherChat, err := runner.CoderContextSummary("chat-compact-2")
	if err != nil {
		t.Fatalf("other-chat read failed: %v", err)
	}
	if len(otherChat) != 0 {
		t.Fatalf("a never-touched chat must report no summary, got %v", otherChat)
	}
}

func TestCoderCompactDropsDuplicatesOldestFirst(t *testing.T) {
	runner := scratchCoderCompactRunner(t)

	if _, err := runner.CompactCoderContext("chat-dedup", "alpha", 0); err != nil {
		t.Fatalf("compact alpha failed: %v", err)
	}
	if _, err := runner.CompactCoderContext("chat-dedup", "beta", 0); err != nil {
		t.Fatalf("compact beta failed: %v", err)
	}
	// Re-folding already-folded material must not duplicate it.
	if _, err := runner.CompactCoderContext("chat-dedup", "alpha", 0); err != nil {
		t.Fatalf("compact alpha again failed: %v", err)
	}

	state, err := runner.CoderContextSummary("chat-dedup")
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	summary := anyString(state["summary"])
	if strings.Count(summary, "alpha") != 1 {
		t.Fatalf("duplicate material was not dropped: %q", summary)
	}
}

func TestCoderCompactEmptyReadWithoutFoldYieldsNothingOnScratch(t *testing.T) {
	// A never-touched chat has no summary; the tool layer reports that as an
	// error, but the runner itself must not panic and must return an empty map.
	runner := scratchCoderCompactRunner(t)
	state, err := runner.CoderContextSummary("chat-never-touched")
	if err != nil {
		t.Fatalf("expected no error for an empty scratch chat, got %v", err)
	}
	if len(state) != 0 {
		t.Fatalf("expected an empty map for a never-touched chat, got %v", state)
	}
}

func anyString(v interface{}) string {
	s, _ := v.(string)
	return s
}