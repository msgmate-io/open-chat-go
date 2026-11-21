package msgmate

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestCI1169ToolCallCapStaysClassifiedWhenWrapped pins the ci#1169 half of the
// ci#1160 fix: a tool-call budget stop is a capacity stop, and it must stay
// recognisable as one all the way to the place that decides how the turn
// settles - including through the wrapping the handler and the queue task
// apply.
//
// The classifier (provider_retry.go) and the terminal metadata
// (turn_outcome.go) already handle the bare typed error; this test pins that
// the same holds for the wrapped forms that actually travel at runtime:
//
//   - a plain %w wrap,
//   - the "response already sent" marker, which the handler returns for every
//     terminal turn that already persisted its final message. Wrapping that
//     marker with %v (the historic form) flattened the typed cap error into a
//     string, so bot_reply re-classified a by-design budget stop as
//     "internal error" and the resumable turn reported a fault.
//
// generation_failure_internal_ci950_test.go stays the counter-pin: a generic
// error must still classify as internal_error, so the cap branch may only match
// the typed error.
func TestCI1169ToolCallCapStaysClassifiedWhenWrapped(t *testing.T) {
	kinds := []string{toolCallCapErrorKindTotal, toolCallCapErrorKindFailed}

	// 1. Both cap kinds classify as the budget reason, never as internal.
	for _, kind := range kinds {
		capErr := newToolCallCapError(kind, 500, 500, 0)
		cls := ClassifyGenerationFailure(capErr)
		if cls.Reason == FailureReasonInternal {
			t.Fatalf("ClassifyGenerationFailure(cap kind %q) Reason = %q, want it NOT to be %q (ci#1169)", kind, cls.Reason, FailureReasonInternal)
		}
		if cls.Reason != FailureReasonToolCallBudget {
			t.Fatalf("ClassifyGenerationFailure(cap kind %q) Reason = %q, want %q", kind, cls.Reason, FailureReasonToolCallBudget)
		}
	}

	// 2. The real error travels wrapped; %w wrapping must not lose the match.
	wrapped := fmt.Errorf("generation failed: %w", newToolCallCapError(toolCallCapErrorKindTotal, 500, 500, 0))
	if cls := ClassifyGenerationFailure(wrapped); cls.Reason != FailureReasonToolCallBudget {
		t.Fatalf("ClassifyGenerationFailure(%%w-wrapped cap) Reason = %q, want %q", cls.Reason, FailureReasonToolCallBudget)
	}

	// 3. The handler's terminal return path keeps the cap error classifiable
	//    through the "response already sent" marker, which is what the queue
	//    task re-classifies when it renders the failure text.
	for _, kind := range kinds {
		capErr := newToolCallCapError(kind, 500, 500, 0)
		terminal := wrapTerminalResponseAlreadySent(capErr)
		if !errors.Is(terminal, ErrResponseAlreadySent) {
			t.Fatalf("wrapTerminalResponseAlreadySent(cap kind %q) lost ErrResponseAlreadySent: %v", kind, terminal)
		}
		if cls := ClassifyGenerationFailure(terminal); cls.Reason != FailureReasonToolCallBudget {
			t.Fatalf("ClassifyGenerationFailure(response-already-sent cap kind %q) Reason = %q, want %q - the wrap must not flatten the typed cap error", kind, cls.Reason, FailureReasonToolCallBudget)
		}
	}

	// 4. A non-cap terminal error keeps the historic wrapping (marker reachable,
	//    error not re-typed), so no unrelated classification changes.
	genericErr := errors.New("boom: unstructured internal fault")
	genericTerminal := wrapTerminalResponseAlreadySent(genericErr)
	if !errors.Is(genericTerminal, ErrResponseAlreadySent) {
		t.Fatalf("wrapTerminalResponseAlreadySent(generic) lost ErrResponseAlreadySent: %v", genericTerminal)
	}
	if cls := ClassifyGenerationFailure(genericTerminal); cls.Reason != FailureReasonInternal {
		t.Fatalf("ClassifyGenerationFailure(wrapped generic) Reason = %q, want %q", cls.Reason, FailureReasonInternal)
	}

	// 5. The terminal assistant text of a cap stop is the existing cap
	//    guidance, not the internal-error wording.
	capErr := newToolCallCapError(toolCallCapErrorKindTotal, 500, 500, 0)
	text := ResolveTerminalAssistantText("", 500, false, capErr, nil)
	if strings.Contains(strings.ToLower(text), "internal error") {
		t.Fatalf("ResolveTerminalAssistantText(cap) = %q, want the cap guidance instead of internal-error wording", text)
	}
	if !strings.Contains(text, "tool-call budget") {
		t.Fatalf("ResolveTerminalAssistantText(cap) = %q, want the tool-call budget guidance", text)
	}
	if want := capErr.guidance(); !strings.Contains(text, want) {
		t.Fatalf("ResolveTerminalAssistantText(cap) = %q, want it to reuse the existing guidance %q", text, want)
	}

	// 6. Handler-level: a turn ending on either cap records the terminal outcome
	//    and does not stamp metadata["error"], so the interaction settles
	//    completed/paused instead of failed.
	for kind, reason := range map[string]string{
		toolCallCapErrorKindTotal:  TurnEndedToolCallCap,
		toolCallCapErrorKindFailed: TurnEndedFailedToolCallCap,
	} {
		k := kind
		r := reason
		metadata := map[string]interface{}{}
		err := newToolCallCapError(k, 500, 500, 0)
		applyTerminalFailureMetadata(metadata, err, ClassifyGenerationFailure(err), &TurnOutcome{Reason: r})
		if errFlag, ok := metadata["error"]; ok && errFlag == true {
			t.Fatalf("cap-stop metadata error = %v, want it unset/false (ci#1169, kind %q)", errFlag, k)
		}
		if got := metadata["turn_ended"]; got != r {
			t.Fatalf("cap-stop metadata turn_ended = %v, want %q (kind %q)", got, r, k)
		}
		if got := metadata["failure_reason"]; got != FailureReasonToolCallBudget {
			t.Fatalf("cap-stop metadata failure_reason = %v, want %q (kind %q)", got, FailureReasonToolCallBudget, k)
		}
	}
}
