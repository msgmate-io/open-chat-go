package chats

import (
	"testing"

	"backend/chatstate"
)

// ci#1179 (follow-up to ci#1160): `finished && error` still settles the
// interaction failed, but a capacity stop the server performed on itself - a
// reached tool-call budget, or a provider-backpressure refusal from OpenChat's
// own admission queue - must settle `finished` so the recovery continuation it
// scheduled stays visible instead of the turn being reported as a defect.
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
			name: "exempted reason: provider backpressure stays finished",
			meta: map[string]interface{}{
				"finished":       true,
				"error":          true,
				"failure_reason": "provider_backpressure",
			},
			want: chatstate.StateFinished,
		},
		{
			name: "backpressure with a scheduled recovery continuation stays finished",
			meta: map[string]interface{}{
				"finished":       true,
				"error":          true,
				"failure_reason": "provider_backpressure",
				InteractionStatusProviderBackpressureContinuationsKey: float64(1),
			},
			want: chatstate.StateFinished,
		},
		{
			name: "backpressure recovery ledger read as int stays finished",
			meta: map[string]interface{}{
				"finished":       true,
				"error":          true,
				"failure_reason": "provider_backpressure",
				InteractionStatusProviderBackpressureContinuationsKey: int(1),
			},
			want: chatstate.StateFinished,
		},
		{
			name: "stamped capacity stop stays finished",
			meta: map[string]interface{}{
				"finished":      true,
				"error":         true,
				"capacity_stop": "provider_backpressure",
			},
			want: chatstate.StateFinished,
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
		{
			name: "error without a capacity marker settles failed",
			meta: map[string]interface{}{
				"finished": true,
				"error":    true,
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

// TestIsNonFaultTerminalFailureReason pins the chats-side copy of the
// capacity-stop exemption. Its authority is the canonical constant set in
// msgmate (msgmate.failureReasonExemptFromErrorFlag, asserted equal by
// msgmate.TestIsCapacityStopReason); chats cannot import it here, so this test
// guards the literal values the copy hardcodes.
func TestIsNonFaultTerminalFailureReason(t *testing.T) {
	for _, reason := range []string{"tool_call_budget_exhausted", "provider_backpressure"} {
		if !IsNonFaultTerminalFailureReason(reason) {
			t.Fatalf("IsNonFaultTerminalFailureReason(%q) = false, want true", reason)
		}
	}
	for _, reason := range []string{"provider_error", "internal_error", "turn_timeout", ""} {
		if IsNonFaultTerminalFailureReason(reason) {
			t.Fatalf("IsNonFaultTerminalFailureReason(%q) = true, want false", reason)
		}
	}
}
