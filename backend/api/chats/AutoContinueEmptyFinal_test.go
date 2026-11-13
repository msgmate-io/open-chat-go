package chats

import "testing"

// TestDecideAutoContinueEmptyFinalTextRetryableOnce guards the ci#1065 loop
// policy: a turn that ran tool calls but settled without a final summary (even
// after the single summary nudge of the tool loop) is retried *once* through
// the bounded incomplete-turn path, because one nudge is cheap. A consecutive
// streak of empty finals is not retried: the model keeps ending empty, so the
// loop reports `empty_final_text_streak` instead of burning its retry budget.
func TestDecideAutoContinueEmptyFinalTextRetryableOnce(t *testing.T) {
	// The classifier itself must accept the first occurrence.
	if !turnEndedIncompleteRetryable(map[string]interface{}{turnEndedMetadataKey: turnEndedEmptyFinalText}) {
		t.Fatalf("turn_ended=%q must be retryable", turnEndedEmptyFinalText)
	}

	config := enabledConfig()

	// First occurrence: the finished turn reports an empty final, no streak
	// counter is stamped (it is the first one), so the loop retries it with the
	// dedicated incomplete-turn prompt and records a streak of one.
	first := finishedMessage(`{"finished":true,"turn_ended":"empty_final_text"}`)
	firstDecision := decideAutoContinue(config, autoContinueState{Turns: 1}, first, decodeOrFail(t, first.MetaData), false)
	if !firstDecision.Continue {
		t.Fatalf("the first empty final must be retried, got stop reason %q", firstDecision.StopReason)
	}
	if firstDecision.Reason != autoContinueIncompleteRetryReason {
		t.Fatalf("Reason = %q, want %q", firstDecision.Reason, autoContinueIncompleteRetryReason)
	}
	if firstDecision.Prompt != config.IncompletePrompt || firstDecision.Prompt == "" {
		t.Fatalf("an empty-final retry must use the dedicated prompt, got %q", firstDecision.Prompt)
	}
	if firstDecision.EmptyFinalStreak != 1 {
		t.Fatalf("EmptyFinalStreak = %d, want 1", firstDecision.EmptyFinalStreak)
	}
	if firstDecision.IncompleteRetries != 1 {
		t.Fatalf("IncompleteRetries = %d, want 1", firstDecision.IncompleteRetries)
	}

	// Second consecutive empty final, carried by the streak metadata stamped by
	// the tool loop: the loop must stop instead of retrying again.
	second := finishedMessage(`{"finished":true,"turn_ended":"empty_final_text","empty_final_streak":2}`)
	secondDecision := decideAutoContinue(config, autoContinueState{Turns: 2}, second, decodeOrFail(t, second.MetaData), false)
	if secondDecision.Continue {
		t.Fatal("a consecutive empty-final streak must not be retried")
	}
	if secondDecision.StopReason != autoContinueEmptyFinalStreakReason {
		t.Fatalf("StopReason = %q, want %q", secondDecision.StopReason, autoContinueEmptyFinalStreakReason)
	}
	if secondDecision.EmptyFinalStreak != 2 {
		t.Fatalf("EmptyFinalStreak = %d, want 2", secondDecision.EmptyFinalStreak)
	}
	if secondDecision.Reason == autoContinueIncompleteRetryReason {
		t.Fatal("a consecutive empty final must not be scheduled as an incomplete retry")
	}

	// The same guard must also work when the turn does not stamp the counter:
	// the persisted loop state plus the turn that just ended reaches two.
	fromState := finishedMessage(`{"finished":true,"turn_ended":"empty_final_text"}`)
	fromStateDecision := decideAutoContinue(config, autoContinueState{Turns: 2, EmptyFinalStreak: 1}, fromState, decodeOrFail(t, fromState.MetaData), false)
	if fromStateDecision.Continue || fromStateDecision.StopReason != autoContinueEmptyFinalStreakReason {
		t.Fatalf("a streak of two derived from the loop state must stop, got %+v", fromStateDecision)
	}
	if fromStateDecision.EmptyFinalStreak != 2 {
		t.Fatalf("EmptyFinalStreak = %d, want 2", fromStateDecision.EmptyFinalStreak)
	}
}

// TestTurnEndedEmptyFinalTextStreak covers the streak resolver behind the
// ci#1065 retry cap: only an empty-final turn carries a streak, and a turn that
// reported its work resets it to zero so it is retried again.
func TestTurnEndedEmptyFinalTextStreak(t *testing.T) {
	// A turn that is not an empty final never carries a streak, whatever the
	// persisted state says.
	for _, reason := range []string{"", "  ", "completed", "no_usable_tool_call"} {
		meta := map[string]interface{}{turnEndedMetadataKey: reason}
		if streak := turnEndedEmptyFinalTextStreak(meta, autoContinueState{EmptyFinalStreak: 3}); streak != 0 {
			t.Errorf("turn_ended=%q: streak = %d, want 0", reason, streak)
		}
	}
	if streak := turnEndedEmptyFinalTextStreak(nil, autoContinueState{EmptyFinalStreak: 3}); streak != 0 {
		t.Errorf("nil metadata: streak = %d, want 0", streak)
	}

	// Without a stamped counter the loop state plus this turn is the streak.
	if streak := turnEndedEmptyFinalTextStreak(map[string]interface{}{turnEndedMetadataKey: turnEndedEmptyFinalText}, autoContinueState{}); streak != 1 {
		t.Errorf("first empty final: streak = %d, want 1", streak)
	}
	if streak := turnEndedEmptyFinalTextStreak(map[string]interface{}{turnEndedMetadataKey: turnEndedEmptyFinalText}, autoContinueState{EmptyFinalStreak: 1}); streak != 2 {
		t.Errorf("second empty final: streak = %d, want 2", streak)
	}

	// A stamped counter wins over the loop state; the numeric types a decoded
	// JSON payload can produce are all accepted.
	for _, stamped := range []interface{}{2, int64(2), float64(2)} {
		meta := map[string]interface{}{
			turnEndedMetadataKey:        turnEndedEmptyFinalText,
			emptyFinalStreakMetadataKey: stamped,
		}
		if streak := turnEndedEmptyFinalTextStreak(meta, autoContinueState{}); streak != 2 {
			t.Errorf("stamped streak %T(%v): streak = %d, want 2", stamped, stamped, streak)
		}
	}
}
