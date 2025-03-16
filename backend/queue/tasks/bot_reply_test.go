package tasks

import (
	"context"
	"errors"
	"testing"
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
			name:     "generic error",
			err:      errors.New("unexpected decoder failure"),
			expected: "I ran into an error while generating a reply. Please try again in a moment.",
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
