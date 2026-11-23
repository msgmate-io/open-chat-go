package msgmate

import (
	"errors"
	"testing"
)

// TestIsCapacityStopReason covers the ci#1160 regression: a capacity stop is a
// turn the server ended on purpose (tool-call budget exhausted, or provider
// admission saturation), so it must never be reported as an internal error.
// The ci#622 native-coder self-check asserted `no_agent_error` for every role
// and failed 5/5 because these stops carried the `error: true` chat metadata.
func TestIsCapacityStopReason(t *testing.T) {
	cases := map[string]bool{
		FailureReasonToolCallBudget:       true,
		FailureReasonProviderBackpressure: true,
		FailureReasonProviderError:        false,
		FailureReasonContextWindow:        false,
		FailureReasonLeakedCompaction:     false,
		FailureReasonRepetitionLoop:       false,
		"":                                false,
		"something_unexpected":            false,
	}

	for reason, want := range cases {
		if got := IsCapacityStopReason(reason); got != want {
			t.Errorf("IsCapacityStopReason(%q) = %v, want %v", reason, got, want)
		}
	}
}

// TestCI1160BackpressureStopIsNotAgentError pins the handler half of the
// ci#1160 fix: a turn that was refused by provider admission backpressure is a
// capacity stop, so applyTerminalFailureMetadata must record it without the
// `error: true` flag that makes api/chats settle the interaction as
// state=failed and makes the ci#622 self-check report `no_agent_error`.
func TestCI1160BackpressureStopIsNotAgentError(t *testing.T) {
	saturation := &ProviderSaturationError{
		Key:            "coder",
		MaxConcurrency: 2,
		InFlight:       2,
		QueueWaiting:   3,
	}

	cls := ClassifyGenerationFailure(saturation)
	if cls.Reason != FailureReasonProviderBackpressure {
		t.Fatalf("ClassifyGenerationFailure(saturation) Reason = %q, want %q", cls.Reason, FailureReasonProviderBackpressure)
	}

	metadata := map[string]interface{}{}
	applyTerminalFailureMetadata(metadata, saturation, cls, &TurnOutcome{Reason: TurnEndedProviderError})
	if errFlag, ok := metadata["error"]; ok && errFlag == true {
		t.Fatalf("backpressure-stop metadata error = %v, want it unset/false (ci#1160)", errFlag)
	}
	if got := metadata["failure_reason"]; got != FailureReasonProviderBackpressure {
		t.Fatalf("backpressure-stop metadata failure_reason = %v, want %q", got, FailureReasonProviderBackpressure)
	}
	if got := metadata["capacity_stop"]; got != FailureReasonProviderBackpressure {
		t.Fatalf("backpressure-stop metadata capacity_stop = %v, want %q (it must stay inspectable)", got, FailureReasonProviderBackpressure)
	}

	// A genuine internal fault keeps the historic error surface, so the self
	// check still catches a real defect.
	internalErr := errors.New("boom: unstructured internal fault")
	internalMetadata := map[string]interface{}{}
	applyTerminalFailureMetadata(internalMetadata, internalErr, ClassifyGenerationFailure(internalErr), &TurnOutcome{})
	if internalMetadata["error"] != true {
		t.Fatalf("internal-failure metadata error = %v, want true", internalMetadata["error"])
	}
	if _, ok := internalMetadata["capacity_stop"]; ok {
		t.Fatalf("internal-failure metadata capacity_stop = %v, want it unset", internalMetadata["capacity_stop"])
	}
}
