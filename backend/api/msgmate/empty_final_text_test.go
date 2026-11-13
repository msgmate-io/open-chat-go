package msgmate

import "testing"

// simulateEmptyFinalSettle replays the ci#1065 settle branch of the tool loop
// (openai.go, "the turn would settle here") for a sequence of rounds: a round
// that carries no assistant text asks the guard for the single synthetic
// summary nudge and otherwise settles with the guard's terminal reason, while
// a round that carries text settles the turn as TurnEndedCompleted and clears
// the empty-final streak. It only calls the production helpers, so the tests
// below pin the loop's decision order instead of re-deriving it.
func simulateEmptyFinalSettle(totalToolCalls int, rounds []string, guard *emptyFinalGuard) (reason string, nudges int) {
	for _, aiResponse := range rounds {
		if !isEmptyFinalRound(totalToolCalls, aiResponse) {
			guard.reported()
			return TurnEndedCompleted, nudges
		}
		if guard.nudge() {
			nudges++
			continue
		}
		return guard.settleReason(), nudges
	}
	return TurnEndedCompleted, nudges
}

// TestEmptyFinalText_NudgeRecovers covers the common case of ci#1065: a turn
// that ran tool calls and would settle empty is nudged once, the nudge gets
// real assistant text back, and the turn does not settle as an empty final.
func TestEmptyFinalText_NudgeRecovers(t *testing.T) {
	guard := &emptyFinalGuard{}

	reason, nudges := simulateEmptyFinalSettle(2, []string{"", "Here is what I did in this turn."}, guard)

	if nudges != 1 {
		t.Fatalf("expected exactly one summary nudge, got %d", nudges)
	}
	if reason != TurnEndedCompleted {
		t.Fatalf("expected the nudged turn to settle as %q, got %q", TurnEndedCompleted, reason)
	}
	if reason == TurnEndedEmptyFinalText {
		t.Fatalf("a recovered turn must not settle as %q", TurnEndedEmptyFinalText)
	}
	if guard.streak != 0 {
		t.Fatalf("a reporting turn must clear the empty-final streak, got %d", guard.streak)
	}
}

// TestEmptyFinalText_SettlesEmptyFinalText covers the failure case of ci#1065:
// the round after the nudge is still empty, so the turn is reported as
// TurnEndedEmptyFinalText instead of finishing on an empty message.
func TestEmptyFinalText_SettlesEmptyFinalText(t *testing.T) {
	guard := &emptyFinalGuard{}

	reason, nudges := simulateEmptyFinalSettle(2, []string{"", "   "}, guard)

	if nudges != 1 {
		t.Fatalf("expected exactly one summary nudge, got %d", nudges)
	}
	if reason != TurnEndedEmptyFinalText {
		t.Fatalf("expected the still-empty turn to settle as %q, got %q", TurnEndedEmptyFinalText, reason)
	}
	if guard.streak != 1 {
		t.Fatalf("expected the empty final to be counted once, got streak %d", guard.streak)
	}
}

// TestEmptyFinalText_NudgeOncePerTurn covers the hard cap of ci#1065: several
// empty rounds of the same turn must not produce more than one nudge, and once
// the nudge is spent every further empty round settles as an empty final.
func TestEmptyFinalText_NudgeOncePerTurn(t *testing.T) {
	guard := &emptyFinalGuard{}

	reason, nudges := simulateEmptyFinalSettle(3, []string{"", "", "", ""}, guard)

	if nudges != 1 {
		t.Fatalf("the summary nudge must be capped at one per turn, got %d", nudges)
	}
	if reason != TurnEndedEmptyFinalText {
		t.Fatalf("expected the turn to settle as %q after the spent nudge, got %q", TurnEndedEmptyFinalText, reason)
	}
	// A second empty round of the same turn must be refused by the guard, so a
	// loop that keeps asking cannot nudge the model forever.
	if guard.nudge() {
		t.Fatal("the guard must refuse a second nudge within the same turn")
	}
}
