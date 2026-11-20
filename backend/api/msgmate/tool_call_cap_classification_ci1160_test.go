package msgmate

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestCI1160ToolCallCapIsNotInternalError pins the ci#1160 requirement that a
// turn stopped by this chat's tool-call budget is a capacity stop and not an
// internal fault.
//
// Before the fix a reached cap was carried as a plain error, so
// ClassifyGenerationFailure fell through to FailureReasonInternal, the handler
// rendered "I hit an internal error ..." and the message was persisted with
// `error: true`. That made a healthy, by-design budget stop indistinguishable
// from a server defect for both the user and the ci#622 native-coder
// no_agent_error self-check. A cap stop must classify as its own reason, still
// match when wrapped, resolve to the existing cap guidance, and be recorded
// without the error flag.
func TestCI1160ToolCallCapIsNotInternalError(t *testing.T) {
	// 1. Both cap kinds classify as the budget reason, never as internal.
	for _, kind := range []string{toolCallCapErrorKindTotal, toolCallCapErrorKindFailed} {
		capErr := newToolCallCapError(kind, 500, 500, 0)
		cls := ClassifyGenerationFailure(capErr)
		if cls.Reason == FailureReasonInternal {
			t.Fatalf("ClassifyGenerationFailure(cap kind %q) Reason = %q, want it NOT to be %q (ci#1160)", kind, cls.Reason, FailureReasonInternal)
		}
		if cls.Reason != FailureReasonToolCallBudget {
			t.Fatalf("ClassifyGenerationFailure(cap kind %q) Reason = %q, want %q", kind, cls.Reason, FailureReasonToolCallBudget)
		}
	}

	// 2. The classification still recognises a wrapped cap error, because the
	//    tool loop hands the terminal error up the stack wrapped in context.
	wrapped := fmt.Errorf("generation failed: %w", newToolCallCapError(toolCallCapErrorKindTotal, 500, 500, 0))
	if cls := ClassifyGenerationFailure(wrapped); cls.Reason != FailureReasonToolCallBudget {
		t.Fatalf("ClassifyGenerationFailure(wrapped cap) Reason = %q, want %q", cls.Reason, FailureReasonToolCallBudget)
	}

	// 3. The terminal assistant text of a cap stop is the cap guidance, not the
	//    internal-error wording.
	capErr := newToolCallCapError(toolCallCapErrorKindTotal, 500, 500, 0)
	text := ResolveTerminalAssistantText("", 500, false, capErr, nil)
	if strings.Contains(strings.ToLower(text), "internal error") {
		t.Fatalf("ResolveTerminalAssistantText(cap) = %q, want cap guidance instead of an internal-error text", text)
	}
	if !strings.Contains(text, "tool-call budget") {
		t.Fatalf("ResolveTerminalAssistantText(cap) = %q, want the tool-call budget guidance", text)
	}

	// 4. Handler-level: a cap-terminated turn keeps turn_ended and the failure
	//    reason but is not flagged as an errored message.
	metadata := map[string]interface{}{}
	outcome := &TurnOutcome{Reason: TurnEndedToolCallCap}
	applyTerminalFailureMetadata(metadata, capErr, ClassifyGenerationFailure(capErr), outcome)
	if errFlag, ok := metadata["error"]; ok && errFlag == true {
		t.Fatalf("cap-stop metadata error = %v, want it unset/false (ci#1160)", errFlag)
	}
	if got := metadata["turn_ended"]; got != TurnEndedToolCallCap {
		t.Fatalf("cap-stop metadata turn_ended = %v, want %q", got, TurnEndedToolCallCap)
	}
	if got := metadata["failure_reason"]; got != FailureReasonToolCallBudget {
		t.Fatalf("cap-stop metadata failure_reason = %v, want %q", got, FailureReasonToolCallBudget)
	}

	// 5. A generic failure still classifies as internal and is still flagged as
	//    an errored message (generation_failure_internal_ci950_test.go keeps the
	//    classification half of this; this pins the metadata half).
	genericErr := errors.New("boom: unstructured internal fault")
	genericMetadata := map[string]interface{}{}
	applyTerminalFailureMetadata(genericMetadata, genericErr, ClassifyGenerationFailure(genericErr), &TurnOutcome{})
	if genericMetadata["error"] != true {
		t.Fatalf("generic-failure metadata error = %v, want true", genericMetadata["error"])
	}
	if got := genericMetadata["failure_reason"]; got != FailureReasonInternal {
		t.Fatalf("generic-failure metadata failure_reason = %v, want %q", got, FailureReasonInternal)
	}
}