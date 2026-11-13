package msgmate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// emptyFinalTextNudgeMarker is a distinctive substring of
// emptyFinalTextNudgePrompt. It is used to count how often the synthetic
// "give me the summary" request actually reached the provider.
const emptyFinalTextNudgeMarker = "did not write a final message for me"

// emptyFinalTextRecorder keeps the raw provider request bodies of a test server
// so a test can assert both how many rounds ran and how often the ci#1065 nudge
// was appended to the transcript.
type emptyFinalTextRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (r *emptyFinalTextRecorder) record(body string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
	return len(r.bodies)
}

func (r *emptyFinalTextRecorder) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *emptyFinalTextRecorder) nudgeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, body := range r.bodies {
		count += strings.Count(body, emptyFinalTextNudgeMarker)
	}
	return count
}

// emptyFinalTextServer reproduces the ci#1065 shape: the first round streams a
// single tool call, and every later round streams whatever chunksForRound
// returns (nil means "the model produced nothing at all"). Provider reasoning
// deltas are never part of the assistant text, so a round that only streams
// reasoning is an empty round too.
func emptyFinalTextServer(t *testing.T, toolName string, chunksForRound func(round int) []map[string]interface{}) (*httptest.Server, *emptyFinalTextRecorder) {
	t.Helper()
	recorder := &emptyFinalTextRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		round := recorder.record(string(body))

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		chunks := chunksForRound(round)
		_, _ = w.Write([]byte(sseFromToolCallChunks(chunks)))
	}))
	t.Cleanup(server.Close)
	_ = toolName
	return server, recorder
}

// emptyFinalTextTurn runs one streamChatCompletion turn against server and
// returns the collected assistant text, the terminal error and the outcome the
// turn loop recorded.
func emptyFinalTextTurn(t *testing.T, server *httptest.Server, outcome *TurnOutcome, toolMap map[string]Tool) (string, error) {
	t.Helper()

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm",
		DefaultToolCallMaxTotal, DefaultToolCallMaxFailed,
		[]map[string]interface{}{{"role": "user", "content": "do the work"}},
		nil, toolMap, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-1065", Outcome: outcome},
	)

	var text strings.Builder
	textDone := make(chan struct{})
	go func() {
		defer close(textDone)
		for chunk := range chunks {
			// Provider reasoning rides the same channel in-band (ci#852); it is
			// not user-visible assistant text (ci#1065).
			if strings.HasPrefix(chunk, reasoningChunkPrefix) {
				continue
			}
			text.WriteString(chunk)
		}
	}()
	go func() {
		for range usage {
		}
	}()
	toolsDone := make(chan struct{})
	go func() {
		defer close(toolsDone)
		for range toolCalls {
		}
	}()

	err := <-errs
	<-textDone
	<-toolsDone
	return text.String(), err
}

// TestTurnAsksOnceForSummaryWhenRoundEndsWithoutFinalText is the ci#1065
// regression for the reported symptom: the turn ran tool calls and then ended
// with no assistant text at all, so the chat could only show "This turn ran N
// tool call(s) but ended without a final summary". The turn loop must now ask
// the model once for the missing summary, and that summary is what the user
// gets.
func TestTurnAsksOnceForSummaryWhenRoundEndsWithoutFinalText(t *testing.T) {
	const summary = "I read the config file and it already contains the new default."
	tool := &orderedRecordingTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}
	server, recorder := emptyFinalTextServer(t, "coder_read", func(round int) []map[string]interface{} {
		if round == 1 {
			return []map[string]interface{}{toolCallChunk("call-1", "coder_read", `{}`)}
		}
		// The round that follows the tool call carries no text; only the round
		// after the nudge produces the summary.
		if round >= 3 {
			return []map[string]interface{}{contentFieldChunk(summary)}
		}
		return nil
	})

	outcome := &TurnOutcome{Reason: TurnEndedCompleted}
	text, err := emptyFinalTextTurn(t, server, outcome, toolMap)
	if err != nil {
		t.Fatalf("the turn must not fail when the model answers the summary request: %v", err)
	}
	if strings.TrimSpace(text) != summary {
		t.Fatalf("expected the requested summary as the turn text, got %q", text)
	}
	if outcome.Reason != TurnEndedCompleted {
		t.Fatalf("expected a completed turn once the summary arrived, got %q", outcome.Reason)
	}
	if got := recorder.nudgeCount(); got != 1 {
		t.Fatalf("expected exactly one summary request in the transcript, got %d", got)
	}
	if got := recorder.requestCount(); got != 3 {
		t.Fatalf("expected tool call, empty round and summary round, got %d provider requests", got)
	}
}

// TestTurnSettlesEmptyFinalTextWhenTheSummaryRequestIsAlsoEmpty proves the
// fallback: when the round after the nudge is still empty (here a reasoning-only
// round, which must count as empty because provider reasoning never becomes
// assistant text), the turn settles as TurnEndedEmptyFinalText instead of
// pretending it completed.
func TestTurnSettlesEmptyFinalTextWhenTheSummaryRequestIsAlsoEmpty(t *testing.T) {
	tool := &orderedRecordingTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}
	server, recorder := emptyFinalTextServer(t, "coder_read", func(round int) []map[string]interface{} {
		if round == 1 {
			return []map[string]interface{}{toolCallChunk("call-1", "coder_read", `{}`)}
		}
		if round >= 3 {
			// Reasoning only: the model thought about it but said nothing.
			return []map[string]interface{}{reasoningFieldChunk("reasoning", "Still thinking about it.")}
		}
		return nil
	})

	outcome := &TurnOutcome{Reason: TurnEndedCompleted}
	text, err := emptyFinalTextTurn(t, server, outcome, toolMap)
	if err != nil {
		t.Fatalf("an empty final text is not a generation failure: %v", err)
	}
	if strings.TrimSpace(text) != "" {
		t.Fatalf("expected no assistant text, got %q", text)
	}
	if outcome.Reason != TurnEndedEmptyFinalText {
		t.Fatalf("expected the turn to settle as %q, got %q", TurnEndedEmptyFinalText, outcome.Reason)
	}
	if got := recorder.nudgeCount(); got != 1 {
		t.Fatalf("expected exactly one summary request in the transcript, got %d", got)
	}
	guidance := ResolveTerminalAssistantText(text, 1, false, nil, outcome)
	if !strings.Contains(guidance, fmt.Sprintf("ran %d tool call(s)", 1)) {
		t.Fatalf("the resolved text must keep reporting the work done, got %q", guidance)
	}
	if !strings.Contains(guidance, "asked for one") {
		t.Fatalf("the resolved text must explain that a summary was already requested, got %q", guidance)
	}
}

// TestTurnRequestsTheSummaryAtMostOnce caps the nudge: a model that keeps
// streaming tool calls and then no text must not be asked again and again. One
// request is allowed, the round after it settles the turn.
func TestTurnRequestsTheSummaryAtMostOnce(t *testing.T) {
	tool := &orderedRecordingTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}
	server, recorder := emptyFinalTextServer(t, "coder_read", func(round int) []map[string]interface{} {
		if round == 1 {
			return []map[string]interface{}{toolCallChunk("call-1", "coder_read", `{}`)}
		}
		return nil
	})

	outcome := &TurnOutcome{Reason: TurnEndedCompleted}
	if _, err := emptyFinalTextTurn(t, server, outcome, toolMap); err != nil {
		t.Fatalf("an empty final text is not a generation failure: %v", err)
	}
	if outcome.Reason != TurnEndedEmptyFinalText {
		t.Fatalf("expected the turn to settle as %q, got %q", TurnEndedEmptyFinalText, outcome.Reason)
	}
	if got := recorder.nudgeCount(); got != 1 {
		t.Fatalf("the summary must be requested exactly once per turn, got %d", got)
	}
	if got := recorder.requestCount(); got != 3 {
		t.Fatalf("expected the turn to stop after the round that followed the single nudge, got %d provider requests", got)
	}
}
