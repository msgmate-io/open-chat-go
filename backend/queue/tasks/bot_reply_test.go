package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/api/msgmate"
)

func TestBotReplyFailureMessage(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "I ran into an error while generating a reply. Please try again in a moment.",
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: "I paused this reply. Send another message when you want me to continue.",
		},
		{
			name:     "provider unavailable",
			err:      errors.New("dial tcp 127.0.0.1:443: connection refused"),
			expected: "I can't reach my AI provider right now. Please check the provider configuration and try again.",
		},
		{
			name:     "api key issue",
			err:      errors.New("missing API key for openai provider"),
			expected: "I can't reach my AI provider right now. Please check the provider configuration and try again.",
		},
		{
			name:     "provider http failure",
			err:      &msgmate.ProviderRequestError{StatusCode: 429, Body: `{"error":{"message":"insufficient_quota"}}`, Attempts: 1},
			expected: "Too many AI requests are queued for this provider key right now, so this reply was stopped before reaching the AI provider. Please lower the number of concurrent requests (or wait a moment) and try again. The work already done is kept.",
		},
		{
			name:     "transient network failure",
			err:      errors.New(`request failed: Post "https://example": connection reset by peer`),
			expected: "I can't reach my AI provider right now. Please check the provider configuration and try again.",
		},
		{
			name:     "context window exceeded",
			err:      errors.New("maximum context length exceeded"),
			expected: "The response exceeded the model's maximum context window. Try a shorter prompt, reduce large pasted content, or run commands that produce less output.",
		},
		{
			name:     "turn deadline reports a timeout, never a pause",
			err:      context.DeadlineExceeded,
			expected: "This turn ran longer than the server's time limit for a single reply and was stopped. The work already done is kept - send another message to continue from here.",
		},
		{
			name:     "generic error names the actual error",
			err:      errors.New("unexpected decoder failure"),
			expected: "I hit an internal error while generating this reply: unexpected decoder failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := botReplyFailureMessage(tt.err)
			if got != tt.expected {
				t.Fatalf("unexpected message\nexpected: %q\nactual:   %q", tt.expected, got)
			}
		})
	}
}

// TestBotReplyTurnStartLogHelpersFallBackOutsideWorker pins the ci#1163
// consume-side log helpers: a handler invoked directly (unit tests, admin
// replays) has no asynq task context, and the turn-start line must still be
// emitted with placeholder values instead of panicking or logging empty fields.
func TestBotReplyTurnStartLogHelpersFallBackOutsideWorker(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"background": context.Background(),
		"nil":        nil,
	} {
		t.Run(name, func(t *testing.T) {
			if got := asynqTaskID(ctx); got != "-" {
				t.Fatalf("asynqTaskID = %q, want %q", got, "-")
			}
			if got := asynqQueueName(ctx); got != "-" {
				t.Fatalf("asynqQueueName = %q, want %q", got, "-")
			}
			if got := asynqRetryCount(ctx); got != 0 {
				t.Fatalf("asynqRetryCount = %d, want 0", got)
			}
		})
	}
}

// TestBotReplyGenerationTraceLines pins the ci#1162 generation-boundary trace:
// the start line has to carry the remaining turn budget (the number that
// separates "the continuation turn started with no time left" from "the turn had
// its full budget and still produced nothing"), and the finished line has to
// record whether generation returned an error. A missing start/finished pair in a
// relayed private-sandbox log is then a definitive statement about where the turn
// stalled.
func TestBotReplyGenerationTraceLines(t *testing.T) {
	deadline, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	start := botReplyGenerationStartLine("chat-uuid", deadline)
	if !strings.HasPrefix(start, "bot-reply generation start: chat=chat-uuid budget=") {
		t.Fatalf("unexpected generation start line: %q", start)
	}
	if !strings.Contains(start, "budget=1m30s") {
		t.Fatalf("generation start line must report the remaining turn budget, got %q", start)
	}

	// Without a task deadline the helper falls back to the configured turn
	// timeout instead of logging a zero budget.
	fallback := botReplyGenerationStartLine("chat-uuid", context.Background())
	if strings.Contains(fallback, "budget=0s") {
		t.Fatalf("generation start line must not report a zero budget without a deadline: %q", fallback)
	}

	if got, want := botReplyGenerationFinishedLine("chat-uuid", nil), "bot-reply generation finished: chat=chat-uuid ok=true"; got != want {
		t.Fatalf("generation finished line = %q, want %q", got, want)
	}
	if got, want := botReplyGenerationFinishedLine("chat-uuid", errors.New("boom")), "bot-reply generation finished: chat=chat-uuid ok=false"; got != want {
		t.Fatalf("generation finished line = %q, want %q", got, want)
	}
}

func TestBotReplyFailureMessageUnclassifiedDoesNotMentionCreditsOrOutage(t *testing.T) {
	got := botReplyFailureMessage(errors.New("unexpected decoder failure"))
	gotLower := strings.ToLower(got)
	if strings.Contains(gotLower, "credit") || strings.Contains(gotLower, "outage") {
		t.Fatalf("unclassified errors must not speculate about credits/outage: %q", got)
	}
	if !strings.Contains(got, "unexpected decoder failure") {
		t.Fatalf("unclassified errors must name the actual error: %q", got)
	}
}

// TestBotReplyCapacityStopMetadataDoesNotFlagAnError pins the queue-side half of
// ci#1160: the failure message a bot posts after a capacity stop must not carry
// `error: true`, because api/chats (and the ci#622 native-coder self-check)
// read that flag as an internal fault and settle the interaction as
// state=failed.
func TestBotReplyCapacityStopMetadataDoesNotFlagAnError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "provider admission backpressure",
			err:  &msgmate.ProviderSaturationError{Key: "coder", MaxConcurrency: 2, InFlight: 2, QueueWaiting: 1},
			want: msgmate.FailureReasonProviderBackpressure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := botFailureMessageMetadata(tc.err)
			if got := metadata["error"]; got == true {
				t.Fatalf("capacity-stop failure metadata error = %v, want unset (ci#1160)", got)
			}
			if got := metadata["finished"]; got != true {
				t.Fatalf("capacity-stop failure metadata finished = %v, want true", got)
			}
			if got := metadata["failure_reason"]; got != tc.want {
				t.Fatalf("capacity-stop failure metadata failure_reason = %v, want %q", got, tc.want)
			}
			if got := metadata["capacity_stop"]; got != tc.want {
				t.Fatalf("capacity-stop failure metadata capacity_stop = %v, want %q", got, tc.want)
			}
		})
	}

	// A genuine internal fault keeps the historic error surface.
	internal := botFailureMessageMetadata(errors.New("unexpected decoder failure"))
	if internal["error"] != true {
		t.Fatalf("internal-failure metadata error = %v, want true", internal["error"])
	}
	if _, ok := internal["capacity_stop"]; ok {
		t.Fatalf("internal-failure metadata capacity_stop = %v, want unset", internal["capacity_stop"])
	}
}

// TestIsBotReplyCancellation distinguishes a genuine user stop from the queue
// task's own deadline: a deadline is a timeout that must take the failure path
// (and never render as a user pause), while context.Canceled is a real stop
// (ci#642).
func TestIsBotReplyCancellation(t *testing.T) {
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()

	cases := []struct {
		name string
		err  error
		ctx  context.Context
		want bool
	}{
		{name: "canceled error", err: context.Canceled, ctx: context.Background(), want: true},
		{name: "canceled context is a stop", err: nil, ctx: cancelCtx, want: true},
		{name: "deadline error is not a stop", err: context.DeadlineExceeded, ctx: deadlineCtx, want: false},
		{name: "deadline context is not a stop", err: nil, ctx: deadlineCtx, want: false},
		{name: "ordinary failure is not a stop", err: errors.New("boom"), ctx: context.Background(), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBotReplyCancellation(tc.err, tc.ctx); got != tc.want {
				t.Fatalf("isBotReplyCancellation(%v, ctx) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
