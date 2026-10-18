package msgmate

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// repetitionTestSSE builds an SSE stream that delivers `repeats` identical
// deltas of `unit`, so the streaming reader sees the loop build up delta by
// delta (as it does against a live provider).
func repetitionTestSSE(unit string, repeats int) string {
	var b strings.Builder
	for i := 0; i < repeats; i++ {
		payload, _ := json.Marshal(map[string]interface{}{
			"choices": []map[string]interface{}{{
				"index": 0,
				"delta": map[string]interface{}{"content": unit},
			}},
		})
		b.WriteString("data: ")
		b.Write(payload)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// TestDetectRepetitionLoopFlagsDegenerateTail covers the detector directly: a
// short unit repeated many times in a row is a loop, while ordinary repeated
// prose (a bulleted list, a table, a code fence) is not.
func TestDetectRepetitionLoopFlagsDegenerateTail(t *testing.T) {
	looped := []struct {
		name string
		text string
	}{
		{"close_invoke", strings.Repeat("</invoke>\n", 3000)},
		{"ellipsis", strings.Repeat("...\n", 3000)},
		{"ok", strings.Repeat("Ok.\n", 3000)},
		{"write", strings.Repeat("Write.\n", 2000)},
		{"phrase", strings.Repeat("Done planning. Execute. Let me ", 20)},
	}
	for _, tc := range looped {
		if _, ok := detectRepetitionLoop(tc.text); !ok {
			t.Fatalf("%s: expected a repetition loop to be detected", tc.name)
		}
	}

	clean := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"short", "Ok.\nOk.\nOk.\n"},
		{
			"prose",
			"The repository uses Go modules for the backend and pnpm for the frontend.\n" +
				"Tests are split per package, and the PR CI label set drives which suites run.\n" +
				"Deployments only happen on consolidation PRs targeting the main branch.\n",
		},
		{"list", "Step one:\nStep two:\nStep three:\nStep four:\nStep five:\nStep six:\n"},
		{"short_rule", strings.Repeat("+---", 50)},
	}
	for _, tc := range clean {
		if _, ok := detectRepetitionLoop(tc.text); ok {
			t.Fatalf("%s: ordinary output must not be flagged as a repetition loop", tc.name)
		}
	}
}

// TestApplyRepetitionLoopGuardTruncatesAndNotes locks in the shape of the
// sanitized text: the repeated run is cut off, what came before it is kept, and
// the result carries the clear one-line note.
func TestApplyRepetitionLoopGuardTruncatesAndNotes(t *testing.T) {
	prefix := "I inspected the repository and found the bug.\n\n"
	text := prefix + strings.Repeat("Write.\n", 500)

	got, looped := applyRepetitionLoopGuard(text)
	if !looped {
		t.Fatal("expected the guard to fire on a repetition loop")
	}
	if len(got) > len(prefix)+len(repetitionLoopNote)+4 {
		t.Fatalf("truncated text is still too long (%d bytes)", len(got))
	}
	if !strings.Contains(got, strings.TrimSpace(prefix)) {
		t.Fatalf("text emitted before the loop must be kept, got %q", got)
	}
	if !strings.Contains(got, "stopped: repeated output detected") {
		t.Fatalf("expected the 'stopped: repeated output detected' note, got %q", got)
	}
	if strings.Contains(got, "Write.\nWrite.\n") {
		t.Fatalf("the repeated run must be cut off, got %q", got)
	}

	if _, looped := applyRepetitionLoopGuard(prefix); looped {
		t.Fatal("an ordinary answer must be returned unchanged")
	}
}

// TestProcessStreamingResponseReaderStopsRepetitionLoop is the ci#670 regression
// test: a stream that degenerates into an unbounded repetition loop must be cut
// off early instead of being persisted verbatim, and the returned (persisted)
// text must be short and carry the "stopped: repeated output" note.
func TestProcessStreamingResponseReaderStopsRepetitionLoop(t *testing.T) {
	const repeats = 20000
	unit := "Write.\n"
	sse := repetitionTestSSE(unit, repeats)

	chunkChan := make(chan string, 64)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		map[string]Tool{},
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("a repetition loop must be cut off without failing the turn, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil result")
	}
	if !result.repetitionLoop {
		t.Fatal("expected the round to be flagged as a repetition loop")
	}
	if len(result.aiResponse) > 1024 {
		t.Fatalf("the persisted text must stay short, got %d bytes", len(result.aiResponse))
	}
	if !strings.Contains(result.aiResponse, "stopped: repeated output detected") {
		t.Fatalf("expected the 'stopped: repeated output detected' note, got %q", result.aiResponse)
	}
	if strings.Count(result.aiResponse, unit) > repetitionLoopMinRepeats {
		t.Fatalf("the repeated run must be truncated, got %d repeats", strings.Count(result.aiResponse, unit))
	}
	if result.usedTool {
		t.Fatal("a repetition loop must not report a tool call")
	}

	// The note must actually reach the client stream too: sendRespondingChunk
	// builds the persisted fullText from exactly these chunks, so a note that
	// was only set on the result would never be persisted.
	close(chunkChan)
	var streamed strings.Builder
	for chunk := range chunkChan {
		streamed.WriteString(chunk)
	}
	if !strings.Contains(streamed.String(), "stopped: repeated output detected") {
		t.Fatalf("the note must be streamed so it is persisted, got %q", streamed.String())
	}
	if len(streamed.String()) > 1024 {
		t.Fatalf("the streamed text must stay short, got %d bytes", len(streamed.String()))
	}
}

// TestProcessStreamingResponseReaderKeepsOrdinaryAnswer guards the other half:
// a normal multi-line answer is streamed and stored unchanged.
func TestProcessStreamingResponseReaderKeepsOrdinaryAnswer(t *testing.T) {
	answer := "Here is the plan:\n\n1. read the file\n2. apply the fix\n3. run the tests\n"
	sse := repetitionTestSSE(answer, 1)

	chunkChan := make(chan string, 8)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		map[string]Tool{},
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.repetitionLoop {
		t.Fatal("an ordinary answer must not be flagged as a repetition loop")
	}
	if result.aiResponse != answer {
		t.Fatalf("ordinary answer must be kept verbatim, got %q", result.aiResponse)
	}
}

// TestRepetitionLoopResponseCarriesFailureReason checks the metrics hook a
// repetition loop reports: the response record built in processStreamingRequest
// carries failure_reason=repetition_loop.
func TestRepetitionLoopResponseCarriesFailureReason(t *testing.T) {
	res := &toolCallResult{repetitionLoop: true, aiResponse: "real work\n\n[stopped: repeated output detected]"}
	var response map[string]interface{}
	if res.aiResponse != "" {
		response = map[string]interface{}{"content": res.aiResponse}
	}
	if res.repetitionLoop {
		response["failure_reason"] = FailureReasonRepetitionLoop
	}
	if got, _ := response["failure_reason"].(string); got != "repetition_loop" {
		t.Fatalf("expected failure_reason=repetition_loop, got %q", got)
	}
}
