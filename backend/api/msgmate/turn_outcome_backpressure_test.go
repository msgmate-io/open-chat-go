package msgmate

import "testing"

// ci#1179: a provider-backpressure stop must not settle the interaction as
// failed while a bounded recovery continuation is scheduled for it, but must
// keep the failure once that single attempt is spent (or could not be enqueued).
func TestClearRecoverableBackpressureFailure(t *testing.T) {
	newMetadata := func() map[string]interface{} {
		return map[string]interface{}{
			"error":                            true,
			"error_detail":                     "provider admission saturated",
			BackpressureRecoverableMetadataKey: true,
		}
	}

	t.Run("scheduled continuation clears the failure", func(t *testing.T) {
		metadata := newMetadata()
		if !clearRecoverableBackpressureFailure(metadata, true) {
			t.Fatalf("expected the failure to be cleared when a continuation is scheduled")
		}
		if _, ok := metadata["error"]; ok {
			t.Fatalf("error flag must be dropped, got %v", metadata["error"])
		}
		if _, ok := metadata["error_detail"]; ok {
			t.Fatalf("error_detail must be dropped")
		}
		if _, ok := metadata[BackpressureRecoverableMetadataKey]; ok {
			t.Fatalf("the recoverable marker must be consumed once the retry is scheduled")
		}
		if got := metadata["provider_backpressure_recovery"]; got != BackpressureRecoveryContinuationScheduled {
			t.Fatalf("expected the decision to be recorded as scheduled, got %v", got)
		}
		// The reason itself stays inspectable.
		if got := metadata["failure_reason"]; got != FailureReasonProviderBackpressure {
			t.Fatalf("failure_reason must stay recorded, got %v", got)
		}
	})

	t.Run("exhausted continuation keeps the failure", func(t *testing.T) {
		metadata := newMetadata()
		metadata["failure_reason"] = FailureReasonProviderBackpressure
		if clearRecoverableBackpressureFailure(metadata, false) {
			t.Fatalf("must not clear the failure when no continuation is scheduled")
		}
		if metadata["error"] != true {
			t.Fatalf("error flag must stay for an exhausted backpressure stop")
		}
		if got := metadata["provider_backpressure_recovery"]; got != nil {
			t.Fatalf("no recovery may be recorded when the retry is exhausted, got %v", got)
		}
	})

	t.Run("non-backpressure failures are untouched", func(t *testing.T) {
		metadata := map[string]interface{}{"error": true}
		if clearRecoverableBackpressureFailure(metadata, true) {
			t.Fatalf("must not clear an unrelated failure")
		}
		if metadata["error"] != true {
			t.Fatalf("unrelated failure must stay")
		}
	})

	t.Run("empty metadata is a no-op", func(t *testing.T) {
		if clearRecoverableBackpressureFailure(nil, true) {
			t.Fatalf("nil metadata must not report a change")
		}
	})
}

// ci#1179: applyTerminalFailureMetadata keeps a backpressure stop flagged as an
// error so the settle path can settle it failed *unless* the recovery seam
// clears the flag after actually scheduling the continuation.
func TestApplyTerminalFailureMetadataProviderBackpressure(t *testing.T) {
	metadata := map[string]interface{}{"finished": true}
	applyTerminalFailureMetadata(metadata, &ProviderSaturationError{}, GenerationFailureClassification{
		Reason: FailureReasonProviderBackpressure,
	}, &TurnOutcome{})

	if metadata["error"] != true {
		t.Fatalf("a fresh backpressure stop must start out flagged as an error")
	}
	if metadata[BackpressureRecoverableMetadataKey] != true {
		t.Fatalf("a backpressure stop must be marked recoverable")
	}
	if !isRecoverableBackpressureStop(metadata) {
		t.Fatalf("isRecoverableBackpressureStop must report the recoverable stop")
	}

	// A non-backpressure provider error must stay a plain, non-recoverable failure.
	other := map[string]interface{}{"finished": true}
	applyTerminalFailureMetadata(other, &ProviderSaturationError{}, GenerationFailureClassification{
		Reason: FailureReasonProviderError,
	}, &TurnOutcome{})
	if isRecoverableBackpressureStop(other) {
		t.Fatalf("an ordinary provider error must not be treated as recoverable backpressure")
	}
}

// ci#1179: the bounded continuation dispatch must schedule exactly one recovery
// turn for a backpressure stop, and stop scheduling once the chain is spent.
func TestCapContinuationForOutcomeProviderBackpressure(t *testing.T) {
	outcome := &TurnOutcome{}
	outcome.record(TurnEndedProviderError)

	first := CapContinuationForOutcome(outcome, true, 0, "chat-uuid")
	if !first.Scheduled {
		t.Fatalf("a backpressure stop must schedule a recovery continuation")
	}
	if first.Kind != TurnContinuationTransient {
		t.Fatalf("expected the transient continuation kind, got %q", first.Kind)
	}

	spent := CapContinuationForOutcome(outcome, true, TurnContinuationMaxAttempts, "chat-uuid")
	if spent.Scheduled {
		t.Fatalf("a spent continuation chain must not schedule another retry")
	}

	notRecoverable := CapContinuationForOutcome(outcome, false, 0, "chat-uuid")
	if notRecoverable.Scheduled {
		t.Fatalf("a provider error without a recoverable failure must not be retried")
	}

	// The backpressure fact travels on the outcome, so the decision holds even
	// when the caller does not pass transientInfra (ci#1179).
	backpressured := &TurnOutcome{Reason: TurnEndedProviderError, ProviderBackpressure: true}
	if !CapContinuationForOutcome(backpressured, false, 0, "chat-uuid").Scheduled {
		t.Fatalf("a backpressure stop must schedule its recovery continuation from the outcome alone")
	}
	if CapContinuationForOutcome(backpressured, false, TurnContinuationMaxAttempts, "chat-uuid").Scheduled {
		t.Fatalf("a spent backpressure chain must not schedule another retry")
	}
	if CapContinuationForOutcome(backpressured, false, 0, "").Scheduled {
		t.Fatalf("a continuation without a chain key must not be scheduled")
	}

	// Once the single bounded retry is spent the shipped metadata sets
	// failure_reason=provider_backpressure with `error=true` and no recovery
	// marker, which settles StateFailed (see backend/api/chats).
	exhausted := map[string]interface{}{"finished": true}
	applyTerminalFailureMetadata(exhausted, &ProviderSaturationError{}, GenerationFailureClassification{
		Reason: FailureReasonProviderBackpressure,
	}, &TurnOutcome{})
	if clearRecoverableBackpressureFailure(exhausted, false) {
		t.Fatalf("an exhausted backpressure chain must not clear the failure")
	}
	if exhausted["error"] != true {
		t.Fatalf("an exhausted backpressure stop must stay flagged as an error")
	}
}

// ci#1179: a scheduled backpressure recovery must not tell the user to retry by
// hand, which is what the plain provider-error guidance does.
func TestWithBackpressureRecoveryNotice(t *testing.T) {
	got := withBackpressureRecoveryNotice("The provider is saturated.")
	if want := "The provider is saturated.\n\n" + backpressureRecoveryNotice; got != want {
		t.Fatalf("expected the notice to be appended, got %q", got)
	}
	if got := withBackpressureRecoveryNotice("   "); got != backpressureRecoveryNotice {
		t.Fatalf("empty text must resolve to the notice alone, got %q", got)
	}
}
