package tools

import "testing"

// TestConfirmableContinuationArmedOnEveryCardApproval is the ci#1147
// regression: a card-originated approval must never end with zero follow-up bot
// turns. The old handler armed the continuation only when the model had set
// continue_after_execute, so an approval of a confirmable action that omitted
// the flag (or a round with several confirmable calls, where only one call can
// be parked at a time) finished with the executed/failed event as the newest
// message and no bot turn at all.
func TestConfirmableContinuationArmedOnEveryCardApproval(t *testing.T) {
	const eventUUID = "confirmable-executed-event"
	if got := confirmableContinuationMessageUUID(eventUUID, false); got != eventUUID {
		t.Fatalf("continuation key = %q, want the event message %q (the continuation must be armed on every card approval, ci#1147)", got, eventUUID)
	}
	if got := confirmableContinuationMessageUUID(eventUUID, true); got != "" {
		t.Fatalf("continuation key = %q for an unlock-required action, want none (the action stays pending)", got)
	}
	if got := confirmableContinuationMessageUUID("", false); got != "" {
		t.Fatalf("continuation key = %q without an event message, want none", got)
	}
}

// TestClassifyConfirmableContinuationCoversEveryDecision pins the single-line
// decision log of ci#1147: resumed_turn, continuation_queued, unlock_required
// and the (unreachable) missing case must be distinguishable, so a chat that is
// left without a bot turn can never look like a healthy one again.
func TestClassifyConfirmableContinuationCoversEveryDecision(t *testing.T) {
	cases := []struct {
		name         string
		resumed      bool
		unlock       bool
		continuation string
		wantOutcome  string
		description  string
	}{
		{"resumed", true, false, "event", continuationOutcomeResumedTurn, "a live park took the decision"},
		{"queued", false, false, "event", continuationOutcomeQueued, "no live park, so the continuation is queued"},
		{"unlock", false, true, "", continuationOutcomeUnlockRequired, "the action waits for the unlock"},
		{"missing", false, false, "", continuationOutcomeMissing, "nothing to continue on: an error path"},
	}
	for _, tc := range cases {
		if got := classifyConfirmableContinuation(tc.resumed, tc.unlock, tc.continuation); got != tc.wantOutcome {
			t.Fatalf("%s: classifyConfirmableContinuation(resumed=%v, unlock=%v, continuation=%q) = %q, want %q (%s)",
				tc.name, tc.resumed, tc.unlock, tc.continuation, got, tc.wantOutcome, tc.description)
		}
	}
}
