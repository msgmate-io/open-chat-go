package msgmate

import (
	"strings"
	"testing"
)

// ci#1179: the continuation decision for provider admission back-pressure.
//
// The lane is separate from the cap/transient lanes and bounded to one
// recovery: the first back-pressure stop of an interaction schedules a delayed
// retry, the second one (same interaction, lane already spent) settles instead.
func TestCapContinuationForOutcomeProviderBackpressureCi1179(t *testing.T) {
	classification := GenerationFailureClassification{Reason: FailureReasonProviderBackpressure}

	t.Run("first backpressure schedules the single recovery", func(t *testing.T) {
		outcome := &TurnOutcome{Reason: TurnEndedProviderError}
		continuation := CapContinuationForOutcome(outcome, classification, false, ContinuationAttempts{Cap: 0, ProviderBackpressure: 0}, "chat-1179")
		if !continuation.Required {
			t.Fatal("expected a required continuation for the first back-pressure stop")
		}
		if continuation.Reason != ContinuationReasonProviderBackpressure {
			t.Fatalf("expected reason %q, got %q", ContinuationReasonProviderBackpressure, continuation.Reason)
		}
		if continuation.DelaySeconds != TurnContinuationProviderBackpressureDelaySeconds {
			t.Fatalf("expected the fixed %ds backoff, got %d", TurnContinuationProviderBackpressureDelaySeconds, continuation.DelaySeconds)
		}
		if continuation.DelaySeconds <= TurnContinuationDelaySeconds {
			t.Fatalf("the back-pressure backoff must be longer than the ordinary continuation (%d)", TurnContinuationDelaySeconds)
		}
		if continuation.CorrelationID != TurnContinuationProviderBackpressureCorrelationID("chat-1179") {
			t.Fatalf("expected the back-pressure correlation id, got %q", continuation.CorrelationID)
		}
		if !strings.HasPrefix(continuation.CorrelationID, ContinuationProviderBackpressureCorrelationPrefix+"-") {
			t.Fatalf("expected the back-pressure correlation prefix, got %q", continuation.CorrelationID)
		}
		if continuation.CorrelationID == TurnContinuationCorrelationID("chat-1179") {
			t.Fatal("the back-pressure lane must not share the cap lane's correlation id")
		}
		if strings.TrimSpace(continuation.Prompt) == "" {
			t.Fatal("the recovery must carry a prompt")
		}
	})

	t.Run("second backpressure in the same interaction is exhausted", func(t *testing.T) {
		outcome := &TurnOutcome{Reason: TurnEndedProviderError}
		continuation := CapContinuationForOutcome(outcome, classification, false, ContinuationAttempts{ProviderBackpressure: maxProviderBackpressureContinuations}, "chat-1179")
		if continuation.Required {
			t.Fatalf("the lane is spent, the stop must stay explicit, got %+v", continuation)
		}
	})

	t.Run("a spent cap lane does not consume the backpressure lane", func(t *testing.T) {
		outcome := &TurnOutcome{Reason: TurnEndedProviderError}
		continuation := CapContinuationForOutcome(outcome, classification, false,
			ContinuationAttempts{Cap: TurnContinuationMaxAttempts, ProviderBackpressure: 0}, "chat-1179")
		if !continuation.Required || continuation.Reason != ContinuationReasonProviderBackpressure {
			t.Fatalf("expected the independent back-pressure lane to still be available, got %+v", continuation)
		}
	})

	t.Run("cancelled or completed turns are never resumed", func(t *testing.T) {
		for _, reason := range []string{TurnEndedCancelled, TurnEndedCompleted} {
			outcome := &TurnOutcome{Reason: reason}
			if continuation := CapContinuationForOutcome(outcome, classification, false, ContinuationAttempts{}, "chat-1179"); continuation.Required {
				t.Fatalf("%s must not be resumed, got %+v", reason, continuation)
			}
		}
	})

	t.Run("an unidentified chain is refused", func(t *testing.T) {
		outcome := &TurnOutcome{Reason: TurnEndedProviderError}
		if continuation := CapContinuationForOutcome(outcome, classification, false, ContinuationAttempts{}, ""); continuation.Required {
			t.Fatalf("expected refusal without a chain key, got %+v", continuation)
		}
	})
}

// ci#1179: the dispatch path is the existing ci#747 one - it must schedule the
// back-pressure recovery with the lane's own backoff and correlation id, and the
// visible metadata must show the recovery.
func TestDispatchCapContinuationProviderBackpressureCi1179(t *testing.T) {
	scheduler := &fakeTurnContinuationScheduler{id: "ci1179-sched"}
	outcome := &TurnOutcome{Reason: TurnEndedProviderError}

	dispatch, err := DispatchCapContinuation(scheduler, "chat-1179", outcome,
		GenerationFailureClassification{Reason: FailureReasonProviderBackpressure},
		false, ContinuationAttempts{}, "chat-1179", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dispatch.Scheduled || scheduler.calls != 1 {
		t.Fatalf("expected exactly one scheduled continuation, got %+v (calls %d)", dispatch, scheduler.calls)
	}
	if dispatch.Reason != ContinuationReasonProviderBackpressure {
		t.Fatalf("expected reason %q, got %q", ContinuationReasonProviderBackpressure, dispatch.Reason)
	}
	if scheduler.delay != TurnContinuationProviderBackpressureDelaySeconds {
		t.Fatalf("expected the lane backoff %ds, got %ds", TurnContinuationProviderBackpressureDelaySeconds, scheduler.delay)
	}
	if scheduler.corrID != TurnContinuationProviderBackpressureCorrelationID("chat-1179") {
		t.Fatalf("expected the lane correlation id, got %q", scheduler.corrID)
	}
	if prompt := scheduler.prompt; !strings.Contains(prompt, "back-pressure") {
		t.Fatalf("the recovery prompt must name the blocker, got %q", prompt)
	}

	visible := CapContinuationVisibleMetadata(dispatch, "chat-1179")
	if got, _ := visible[TurnContinuationScheduledMetadataKey].(string); got != ContinuationReasonProviderBackpressure {
		t.Fatalf("expected %s=%q, got %q", TurnContinuationScheduledMetadataKey, ContinuationReasonProviderBackpressure, got)
	}
	if got, _ := visible["continuation_schedule_id"].(string); got != "ci1179-sched" {
		t.Fatalf("expected the schedule id, got %q", got)
	}

	// The spent lane is carried on the stopped message so the next stop of the
	// same interaction settles instead of retrying.
	metadata := map[string]interface{}{ProviderBackpressureContinuationsMetadataKey: 1}
	if got := ProviderBackpressureContinuationsFromMetadata(metadata); got != 1 {
		t.Fatalf("expected 1 spent back-pressure attempt, got %d", got)
	}
	if got := ProviderBackpressureContinuationsFromMetadata(nil); got != 0 {
		t.Fatalf("expected 0 for a turn without metadata, got %d", got)
	}
}

// ci#1179: the exhausted attempt is carried by the scheduled turn's inherited
// correlation id (the scheduler carries no metadata), so the lane must be
// recognised from it.
func TestProviderBackpressureContinuationsFromCorrelationCi1179(t *testing.T) {
	metadata := map[string]interface{}{
		"coder_schedule_scheduled_by_correlation": TurnContinuationProviderBackpressureCorrelationID("chat-1179"),
	}
	if got := ProviderBackpressureContinuationsFromMetadata(metadata); got != 1 {
		t.Fatalf("expected the spent back-pressure lane from the correlation id, got %d", got)
	}
	// A cap-lane correlation id must not spend the back-pressure lane.
	capLane := map[string]interface{}{
		"coder_schedule_scheduled_by_correlation": TurnContinuationCorrelationID("chat-1179"),
	}
	if got := ProviderBackpressureContinuationsFromMetadata(capLane); got != 0 {
		t.Fatalf("a cap continuation must not consume the back-pressure lane, got %d", got)
	}
}
