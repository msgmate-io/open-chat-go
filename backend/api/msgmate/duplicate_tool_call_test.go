package msgmate

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

// duplicateCallRecordingTool counts how often it was actually dispatched, so a
// test can prove that a replayed call was served from the per-turn cache instead
// of running again.
type duplicateCallRecordingTool struct {
	*BaseTool
	runs int
}

func (t *duplicateCallRecordingTool) RunTool(input interface{}) (string, error) {
	t.runs++
	return "fresh result", nil
}

// TestFrameDuplicateToolCallResultIsExplicit locks in the shape of the frame
// (ci#728): the replay must be visibly a replay - it opens with a notice naming
// the tool, keeps the replayed payload verbatim, and closes with the
// "do not re-issue" instruction plus a tool-specific alternative.
func TestFrameDuplicateToolCallResultIsExplicit(t *testing.T) {
	const payload = "line one\nline two\n"
	framed := FrameDuplicateToolCallResult("coder_read", payload)

	if !strings.Contains(framed, "duplicate tool call") {
		t.Fatalf("the frame must say it is a duplicate call, got %q", framed)
	}
	if !strings.Contains(framed, "coder_read") {
		t.Fatalf("the frame must name the tool that was not re-executed, got %q", framed)
	}
	if !strings.Contains(framed, payload) {
		t.Fatalf("the replayed payload must be preserved verbatim, got %q", framed)
	}
	if !strings.Contains(framed, "NOT executed again") {
		t.Fatalf("the frame must state that the call was not executed again, got %q", framed)
	}
	if !strings.Contains(framed, "Do NOT re-issue it") {
		t.Fatalf("the frame must forbid re-issuing the identical call, got %q", framed)
	}
	if !strings.Contains(framed, "offset") || !strings.Contains(framed, "limit") {
		t.Fatalf("a coder_read replay must point at the narrower range, got %q", framed)
	}
	if !strings.HasSuffix(strings.TrimSpace(framed), "]") {
		t.Fatalf("the instruction must be the last thing the model reads, got %q", framed)
	}
	// The instruction must come *after* the payload.
	if strings.Index(framed, "Do NOT re-issue it") < strings.Index(framed, payload) {
		t.Fatalf("the instruction must follow the replayed payload, got %q", framed)
	}
}

// TestFrameDuplicateToolCallResultGuardsEmptyAndUnknownTool covers the edges: an
// empty replayed result still produces a self-explanatory frame, and an unknown
// tool falls back to the generic narrowing hint instead of an empty sentence.
func TestFrameDuplicateToolCallResultGuardsEmptyAndUnknownTool(t *testing.T) {
	empty := FrameDuplicateToolCallResult("coder_bash", "   ")
	if !strings.Contains(empty, "no output") {
		t.Fatalf("an empty replay must say so, got %q", empty)
	}

	unknown := FrameDuplicateToolCallResult("some_future_tool", "payload")
	if !strings.Contains(unknown, "some_future_tool") {
		t.Fatalf("an unknown tool must still be named, got %q", unknown)
	}
	if !strings.Contains(unknown, "narrow the range") {
		t.Fatalf("an unknown tool must get the generic narrowing hint, got %q", unknown)
	}
	if strings.Contains(unknown, "%s") {
		t.Fatalf("the hint placeholder must never leak, got %q", unknown)
	}
}

// TestReplayedToolCallRoundCarriesDuplicateFrame is the ci#728 regression: when
// the model re-issues an identical call in the same turn, the second call must
// not be dispatched again and the result the model reads back must carry the
// duplicate-call frame, while the payload itself stays intact.
func TestReplayedToolCallRoundCarriesDuplicateFrame(t *testing.T) {
	tool := &duplicateCallRecordingTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}
	executed := map[string]string{}

	callSSE := sseFromToolCallChunks([]map[string]interface{}{
		interleavedToolCallChunk(entryWithIndex(0, "call-1", "coder_read", `{"path":"notes.md"}`)),
	})

	readRound := func(roundID string) *toolCallResult {
		t.Helper()
		// The reader keys the cache by content signature, so the round id has
		// to be part of the call id only - the arguments stay identical.
		sse := strings.ReplaceAll(callSSE, "call-1", roundID)
		// Generous buffers: the reader streams into these channels and the test
		// never drains them, so they must not fill up mid-round.
		chunkChan := make(chan string, 64)
		usageChan := make(chan *TokenUsage, 8)
		toolChan := make(chan ToolCall, 64)
		result, err := processStreamingResponseReader(
			context.Background(),
			bufio.NewReader(strings.NewReader(sse)),
			toolMap,
			executed,
			chunkChan,
			usageChan,
			toolChan,
		)
		if err != nil {
			t.Fatalf("round %s: stream processing failed: %v", roundID, err)
		}
		return result
	}

	first := readRound("call-1")
	if tool.runs != 1 {
		t.Fatalf("the first call must be dispatched, got %d runs", tool.runs)
	}
	if strings.Contains(first.result, "duplicate tool call") {
		t.Fatalf("a fresh call must not be framed as a duplicate, got %q", first.result)
	}

	second := readRound("call-2")
	if tool.runs != 1 {
		t.Fatalf("an identical replay must not be dispatched again, got %d runs", tool.runs)
	}
	if !strings.Contains(second.result, "duplicate tool call") {
		t.Fatalf("the replay must carry the duplicate-call frame, got %q", second.result)
	}
	// The first result was cached under the same signature, so the replay must
	// hand back exactly the payload the model already has.
	if !strings.Contains(second.result, first.result) {
		t.Fatalf("the replay must preserve the cached payload, got %q", second.result)
	}
	if len(second.calls) != 1 || second.calls[0].status != ToolCallStatusSucceeded {
		t.Fatalf("the replay must be reported as one succeeded call, got %+v", second.calls)
	}
	if !strings.Contains(second.calls[0].result, "duplicate tool call") {
		t.Fatalf("the transcript version must carry the frame, got %q", second.calls[0].result)
	}
	// A later stage (no-progress guard, ci#728 item 4) must be able to tell a
	// replay from a fresh execution without parsing the frame text.
	if !second.duplicateToolCall {
		t.Fatal("the replay round must be flagged as a duplicate tool call")
	}
	if first.duplicateToolCall {
		t.Fatal("a fresh round must not be flagged as a duplicate tool call")
	}
}
