package msgmate

import "testing"

// ci#747 (RC2/RC3): a cap or transient failure continues exactly once, a clean
// or cancelled turn never does, and the correlation id is stable so the retry
// cancels instead of duplicating.
func TestCapContinuationForOutcome(t *testing.T) {
	tests := []struct {
		name           string
		outcome        *TurnOutcome
		transientInfra bool
		attempts       int
		chainKey       string
		want           bool
		reason         string
	}{
		{
			name:     "failed tool call cap continues once",
			outcome:  &TurnOutcome{Reason: TurnEndedFailedToolCallCap, FailedToolCalls: 60, TransientFailures: 12},
			chainKey: "chat-1",
			want:     true,
			reason:   TurnContinuationFailedToolCap,
		},
		{
			name:     "second attempt of the same chain stops",
			outcome:  &TurnOutcome{Reason: TurnEndedFailedToolCallCap},
			attempts: TurnContinuationMaxAttempts,
			chainKey: "chat-1",
			want:     false,
		},
		{
			name:     "total cap continues",
			outcome:  &TurnOutcome{Reason: TurnEndedToolCallCap, ToolCalls: 500},
			chainKey: "chat-1",
			want:     true,
			reason:   TurnContinuationToolCap,
		},
		{
			name:           "provider error with transient failures continues",
			outcome:        &TurnOutcome{Reason: TurnEndedProviderError, TransientFailures: 1},
			transientInfra: true,
			chainKey:       "chat-1",
			want:           true,
			reason:         TurnContinuationTransient,
		},
		{
			name:     "provider error without transient failure stays explicit",
			outcome:  &TurnOutcome{Reason: TurnEndedProviderError},
			chainKey: "chat-1",
			want:     false,
		},
		{
			name:     "completed turn is never continued",
			outcome:  &TurnOutcome{Reason: TurnEndedCompleted},
			chainKey: "chat-1",
			want:     false,
		},
		{
			name:     "cancelled turn is never continued",
			outcome:  &TurnOutcome{Reason: TurnEndedCancelled},
			chainKey: "chat-1",
			want:     false,
		},
		{
			name:     "confirmable pause is never continued",
			outcome:  &TurnOutcome{Reason: TurnEndedConfirmablePause},
			chainKey: "chat-1",
			want:     false,
		},
		{
			name:     "nil outcome is never continued",
			chainKey: "chat-1",
			want:     false,
		},
		{
			name:    "anonymous chain is never continued",
			outcome: &TurnOutcome{Reason: TurnEndedFailedToolCallCap},
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CapContinuationForOutcome(tc.outcome, tc.transientInfra, tc.attempts, tc.chainKey)
			if got.Required != tc.want {
				t.Fatalf("Required = %t, want %t (%+v)", got.Required, tc.want, got)
			}
			if !tc.want {
				if got != (TurnContinuation{}) {
					t.Fatalf("a rejected continuation must be the zero value, got %+v", got)
				}
				return
			}
			if got.Reason != tc.reason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tc.reason)
			}
			if got.Prompt == "" {
				t.Fatal("a required continuation must carry a prompt")
			}
			if got.CorrelationID != TurnContinuationCorrelationID(tc.chainKey) {
				t.Fatalf("CorrelationID = %q, want the stable id of %q", got.CorrelationID, tc.chainKey)
			}
		})
	}
}

// The correlation id must be stable for a chain and distinct across chains,
// otherwise a retry duplicates work instead of replacing it (RC7).
func TestTurnContinuationCorrelationIDIsStableAndScoped(t *testing.T) {
	if a, b := TurnContinuationCorrelationID("ci-747"), TurnContinuationCorrelationID("ci-747"); a != b {
		t.Fatalf("correlation id is not stable: %q != %q", a, b)
	}
	if a, b := TurnContinuationCorrelationID("ci-747"), TurnContinuationCorrelationID("ci-748"); a == b {
		t.Fatalf("correlation id is not chain-scoped: %q", a)
	}
}
