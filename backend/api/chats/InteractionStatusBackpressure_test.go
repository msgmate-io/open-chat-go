package chats

import (
	"testing"

	"backend/chatstate"
)

// ci#1179: `finished && error` settles the interaction failed, and only the
// reasons that are genuine capacity stops (a reached tool-call budget, or a
// provider-backpressure stop whose single bounded recovery turn was actually
// scheduled) are exempt.
func TestInteractionStateForFinishedMetadata(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]interface{}
		want chatstate.State
	}{
		{
			name: "finished without error settles finished",
			meta: map[string]interface{}{"finished": true},
			want: chatstate.StateFinished,
		},
		{
			name: "exempted reason: tool call budget stays finished",
			meta: map[string]interface{}{
				"finished":       true,
				"error":          true,
				"failure_reason": "tool_call_budget_exhausted",
			},
			want: chatstate.StateFinished,
		},
		{
			name: "backpressure recovered by a scheduled continuation stays finished",
			meta: map[string]interface{}{
				"finished":                       true,
				"error":                          true,
				"failure_reason":                 "provider_backpressure",
				"provider_backpressure_recovery": "continuation_scheduled",
			},
			want: chatstate.StateFinished,
		},
		{
			name: "backpressure with the bounded retry exhausted settles failed",
			meta: map[string]interface{}{
				"finished":                          true,
				"error":                             true,
				"failure_reason":                    "provider_backpressure",
				"provider_backpressure_recoverable": true,
			},
			want: chatstate.StateFailed,
		},
		{
			name: "non-exempted provider error settles failed",
			meta: map[string]interface{}{
				"finished":       true,
				"error":          true,
				"failure_reason": "provider_error",
			},
			want: chatstate.StateFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := interactionStateForFinishedMetadata(tc.meta); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
