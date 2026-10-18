package msgmate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/nativecoder"
)

// ci709CapServer answers every provider request with one tool call for toolName
// (a fresh call id per round), so the tool loop keeps running the tool until its
// per-chat cap stops it. It reproduces the reported symptom shape: many tool
// calls, no assistant text at all.
func ci709CapServer(t *testing.T, toolName string) *httptest.Server {
	t.Helper()
	round := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		payload := sseFromToolCallChunks([]map[string]interface{}{
			toolCallChunk(fmt.Sprintf("call-%d", round), toolName, `{}`),
		})
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestToolCallCapEndsTurnWithResolvedTextAndStructuredCounters is the ci#709
// regression for the "work done but no final message" symptom: a turn that hits
// its per-chat tool-call cap emits a typed cap error and a recorded outcome with
// the consumed/remaining counters, and the terminal message resolution produces
// a non-empty, human-readable summary instead of an empty bubble.
func TestToolCallCapEndsTurnWithResolvedTextAndStructuredCounters(t *testing.T) {
	tool := &orderedRecordingTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}
	server := ci709CapServer(t, "coder_read")

	budget := nativecoder.ToolCallBudget{
		MaxTotal:          4,
		MaxFailed:         3,
		Source:            nativecoder.ToolCallBudgetSourceChatSnapshot,
		SnapshotMaxTotal:  4,
		SnapshotMaxFailed: 3,
	}
	outcome := &TurnOutcome{Reason: TurnEndedCompleted, Budget: budget}

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm",
		DefaultToolCallMaxTotal, DefaultToolCallMaxFailed,
		[]map[string]interface{}{{"role": "user", "content": "do the work"}},
		nil, toolMap, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-709", Budget: &budget, Outcome: outcome},
	)

	assistantText := ""
	go func() {
		for chunk := range chunks {
			assistantText += chunk
		}
	}()
	go func() {
		for range usage {
		}
	}()
	// The tool channel carries an "ongoing" frame and a terminal frame per call,
	// so count distinct call ids to get the number of tool calls the turn ran.
	callIDs := map[string]struct{}{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for frame := range toolCalls {
			if strings.TrimSpace(frame.Id) != "" {
				callIDs[frame.Id] = struct{}{}
			}
		}
	}()

	err := <-errs
	<-done
	registered := len(callIDs)

	if err == nil {
		t.Fatal("expected the turn to end on its per-chat tool-call cap")
	}
	var capErr *toolCallCapError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected a typed tool-call cap error, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "exceeded maximum number of tool calls") {
		t.Fatalf("the historic cap wording was lost: %v", err)
	}
	// The budget is now visible in the error itself, not only implicitly.
	if !strings.Contains(err.Error(), "4") {
		t.Fatalf("expected the cap in the cap error, got %v", err)
	}

	if registered != 4 {
		t.Fatalf("expected the 4 tool calls of the chat budget, got %d", registered)
	}
	if outcome.Reason != TurnEndedToolCallCap {
		t.Fatalf("expected the terminal reason to be the tool-call cap, got %q", outcome.Reason)
	}
	if outcome.ToolCalls != 4 {
		t.Fatalf("expected 4 consumed tool calls in the outcome, got %d", outcome.ToolCalls)
	}

	usageMetadata := outcome.usage().Metadata()
	if usageMetadata["consumed_total"] != 4 || usageMetadata["remaining_total"] != 0 {
		t.Fatalf("expected structured consumed/remaining counters, got %#v", usageMetadata)
	}
	if usageMetadata["exhausted_total"] != true {
		t.Fatalf("expected the total budget to be reported exhausted, got %#v", usageMetadata)
	}
	budgetMetadata := outcome.Budget.Metadata()
	if budgetMetadata["max_total"] != 4 || budgetMetadata["source"] != nativecoder.ToolCallBudgetSourceChatSnapshot {
		t.Fatalf("expected the resolved budget (with its source) in metadata, got %#v", budgetMetadata)
	}
	if budgetMetadata["snapshot_max_total"] != 4 {
		t.Fatalf("expected the per-chat snapshot in metadata, got %#v", budgetMetadata)
	}

	// The reported symptom: assistant text was empty while many tool calls ran.
	if strings.TrimSpace(assistantText) != "" {
		t.Fatalf("this fixture must not stream assistant text, got %q", assistantText)
	}
	finalText := ResolveTerminalAssistantText(assistantText, registered, false, err, outcome)
	if strings.TrimSpace(finalText) == "" {
		t.Fatal("a turn that ran tool calls must never end with an empty final text (ci#709)")
	}
	for _, fragment := range []string{
		"4 tool call(s)",
		"tool-call budget",
		"send another message",
	} {
		if !strings.Contains(finalText, fragment) {
			t.Fatalf("expected the terminal text to contain %q, got %q", fragment, finalText)
		}
	}
}

// TestTerminalTextNeverEmptyOnEveryExitPath covers every terminal path the
// ticket names (cap reached, provider error, cancel, loop guard) plus the
// confirmable pause, and asserts each resolves non-empty, human-readable text.
func TestTerminalTextNeverEmptyOnEveryExitPath(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		toolCalls int
		cancelled bool
		streamErr error
		outcome   *TurnOutcome
		wantAny   []string
	}{
		{
			name:      "tool call cap reached",
			toolCalls: 62,
			streamErr: newToolCallCapError(toolCallCapErrorKindTotal, 100, 100, 1),
			outcome:   &TurnOutcome{Reason: TurnEndedToolCallCap, ToolCalls: 100, FailedToolCalls: 1},
			wantAny:   []string{"62", "tool-call budget"},
		},
		{
			name:      "failed tool call cap reached",
			toolCalls: 7,
			streamErr: newToolCallCapError(toolCallCapErrorKindFailed, 3, 7, 3),
			outcome:   &TurnOutcome{Reason: TurnEndedFailedToolCallCap, ToolCalls: 7, FailedToolCalls: 3},
			wantAny:   []string{"failed tool call", "budget"},
		},
		{
			name:      "provider error",
			toolCalls: 12,
			streamErr: &ProviderRequestError{StatusCode: 429, Body: `{"error":{"message":"rate limited"}}`, Attempts: 3},
			outcome:   &TurnOutcome{Reason: TurnEndedProviderError, ToolCalls: 12},
			wantAny:   []string{"12", "provider"},
		},
		{
			name:      "cancel without text",
			cancelled: true,
			toolCalls: 30,
			outcome:   &TurnOutcome{Reason: TurnEndedCancelled, ToolCalls: 30},
			wantAny:   []string{"30", "continue"},
		},
		{
			name:      "cancel with no tool calls",
			cancelled: true,
			outcome:   &TurnOutcome{Reason: TurnEndedCancelled},
			wantAny:   []string{"paused this response"},
		},
		{
			name:      "cancel with a tool-less bare pause marker",
			text:      "I paused this response. Send another message when you want me to continue.",
			cancelled: true,
			outcome:   &TurnOutcome{Reason: TurnEndedCancelled},
			wantAny:   []string{"paused this response"},
		},
		{
			name:      "cancel with tools and only the bare pause marker",
			text:      "I paused this response. Send another message when you want me to continue.",
			cancelled: true,
			toolCalls: 41,
			outcome:   &TurnOutcome{Reason: TurnEndedCancelled, ToolCalls: 41},
			wantAny:   []string{"41 tool call(s)"},
		},
		{
			name:      "loop guard (no usable call)",
			toolCalls: 5,
			outcome:   &TurnOutcome{Reason: TurnEndedNoUsableCall, ToolCalls: 5},
			wantAny:   []string{"5", "continue"},
		},
		{
			name:    "loop guard with no tool calls",
			outcome: &TurnOutcome{Reason: TurnEndedNoUsableCall},
			wantAny: []string{"could not produce a reply"},
		},
		{
			name:      "context window",
			toolCalls: 3,
			streamErr: errors.New("This model's maximum context length is 8192 tokens"),
			outcome:   &TurnOutcome{Reason: TurnEndedProviderError, ToolCalls: 3},
			wantAny:   []string{"context window"},
		},
		{
			name:      "leaked compaction",
			toolCalls: 9,
			streamErr: errLeakedCompactionTranscript,
			outcome:   &TurnOutcome{Reason: TurnEndedProviderError, ToolCalls: 9},
			wantAny:   []string{"work notes"},
		},
		{
			name:      "confirmable pause",
			toolCalls: 2,
			outcome:   &TurnOutcome{Reason: TurnEndedConfirmablePause, ToolCalls: 2},
			wantAny:   []string{"confirm"},
		},
		{
			name:      "nil outcome after tool calls",
			toolCalls: 4,
			wantAny:   []string{"4 tool call(s)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveTerminalAssistantText(tc.text, tc.toolCalls, tc.cancelled, tc.streamErr, tc.outcome)
			if strings.TrimSpace(got) == "" {
				t.Fatal("the terminal text must never be empty")
			}
			if len(tc.wantAny) == 0 {
				return
			}
			for _, fragment := range tc.wantAny {
				if !strings.Contains(got, fragment) {
					t.Fatalf("expected %q to contain %q", got, fragment)
				}
			}
		})
	}
}

// TestTerminalTextKeepsRealAssistantText makes sure the guarantee only ever
// fills a genuinely blank message: a turn with an answer keeps it verbatim.
func TestTerminalTextKeepsRealAssistantText(t *testing.T) {
	answer := "Done: the branch is pushed and the PR is open."
	if got := ResolveTerminalAssistantText("  "+answer+"\n", 12, false, nil, &TurnOutcome{Reason: TurnEndedCompleted, ToolCalls: 12}); got != answer {
		t.Fatalf("expected the assistant text to be preserved, got %q", got)
	}
	cancelled := ResolveTerminalAssistantText(answer, 12, true, nil, &TurnOutcome{Reason: TurnEndedCancelled})
	if !strings.Contains(cancelled, answer) || !strings.Contains(cancelled, "paused") {
		t.Fatalf("a stop must keep the answer and add the pause marker, got %q", cancelled)
	}
}
