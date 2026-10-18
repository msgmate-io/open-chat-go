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
			expected: "I could not generate a reply: my AI provider request failed (HTTP 429 (insufficient_quota)). Please try again in a moment or switch to another model.",
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
