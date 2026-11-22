package msgmate

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"backend/chatstate"
)

// TestCI1160WrappingKeepsCI1179Exemption pins the interaction between the
// ci#1160 `%w` wrapping of terminal errors and the ci#1179 error-flag
// exemption. ci#1160 made every terminal error reachable through errors.Is /
// errors.As by wrapping it with `%w` instead of flattening it with `%v`; ci#1179
// relies on the classification that wrapping preserves to keep
// `metadata["error"]` unset for the two capacity stops. Both properties have to
// hold at the same time:
//
//   - a terminal error that survives wrapTerminalResponseAlreadySent still
//     classifies as tool_call_budget_exhausted / provider_backpressure, and the
//     finished message therefore carries no `error` flag (so
//     chats.InteractionStatus resolves StateFinished and the ci#1179 recovery
//     continuation stays visible);
//   - every other reason still sets `error: true` after the same wrapping, so a
//     finished message resolves StateFailed and a real fault is never hidden.
func TestCI1160WrappingKeepsCI1179Exemption(t *testing.T) {
	exempt := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "tool-call budget",
			err:  newToolCallCapError(toolCallCapErrorKindTotal, 500, 500, 0),
			want: FailureReasonToolCallBudget,
		},
		{
			name: "provider back-pressure (saturation)",
			err: &ProviderSaturationError{
				Key:            "coder_sandbox",
				MaxConcurrency: 2,
				InFlight:       2,
				QueueWaiting:   3,
				Waited:         250 * time.Millisecond,
			},
			want: FailureReasonProviderBackpressure,
		},
	}
	for _, tc := range exempt {
		// The task re-classifies the terminal error after the handler wrapped it.
		wrapped := wrapTerminalResponseAlreadySent(tc.err)
		if !errors.Is(wrapped, ErrResponseAlreadySent) {
			t.Fatalf("%s: wrapped terminal error no longer matches ErrResponseAlreadySent", tc.name)
		}
		cls := ClassifyGenerationFailure(wrapped)
		if cls.Reason != tc.want {
			t.Fatalf("%s: ClassifyGenerationFailure(wrapped) = %q, want %q (ci#1160 wrapping must keep the typed error reachable)", tc.name, cls.Reason, tc.want)
		}

		metadata := map[string]interface{}{"finished": true}
		applyTerminalFailureMetadata(metadata, wrapped, cls, &TurnOutcome{Reason: TurnEndedProviderError})
		if flag, ok := metadata["error"]; ok && flag == true {
			t.Fatalf("%s: metadata error = %v, want it unset (ci#1179 exemption)", tc.name, flag)
		}
		if _, ok := metadata["error_detail"]; ok {
			t.Fatalf("%s: metadata error_detail is set, want it unset for an exempt reason", tc.name)
		}
		if metadata["failure_reason"] != tc.want {
			t.Fatalf("%s: failure_reason = %v, want %q", tc.name, metadata["failure_reason"], tc.want)
		}
		if state := resolveFinishedMessageState(metadata); state != chatstate.StateFinished {
			t.Fatalf("%s: finished message resolves %q, want %q so the recovery continuation is visible", tc.name, state, chatstate.StateFinished)
		}
	}

	faults := []struct {
		name string
		err  error
		want string
	}{
		{name: "turn deadline", err: context.DeadlineExceeded, want: FailureReasonTimeout},
		{name: "internal", err: errors.New("boom"), want: FailureReasonInternal},
		{name: "leaked compaction", err: errLeakedCompactionTranscript, want: FailureReasonLeakedCompaction},
	}
	for _, tc := range faults {
		wrapped := wrapTerminalResponseAlreadySent(tc.err)
		cls := ClassifyGenerationFailure(wrapped)
		if cls.Reason != tc.want {
			t.Fatalf("%s: ClassifyGenerationFailure(wrapped) = %q, want %q", tc.name, cls.Reason, tc.want)
		}
		if failureReasonExemptFromErrorFlag(cls.Reason) {
			t.Fatalf("%s: reason %q must not be exempt from the error flag", tc.name, cls.Reason)
		}

		metadata := map[string]interface{}{"finished": true}
		applyTerminalFailureMetadata(metadata, wrapped, cls, &TurnOutcome{Reason: TurnEndedProviderError})
		if flag, _ := metadata["error"].(bool); !flag {
			t.Fatalf("%s: metadata error = %v, want true for a genuine failure", tc.name, metadata["error"])
		}
		if detail, _ := metadata["error_detail"].(string); detail == "" {
			t.Fatalf("%s: metadata error_detail is empty, want the wrapped error text", tc.name)
		}
		if state := resolveFinishedMessageState(metadata); state != chatstate.StateFailed {
			t.Fatalf("%s: finished message resolves %q, want %q", tc.name, state, chatstate.StateFailed)
		}
	}
}

// TestCI1160WrappedExemptionSurvivesDoubleWrapping guards the queue hop: the
// task wraps the terminal error once more before classifying it, and the
// ci#1179 exemption has to survive that as well.
func TestCI1160WrappedExemptionSurvivesDoubleWrapping(t *testing.T) {
	inner := wrapTerminalResponseAlreadySent(newToolCallCapError(toolCallCapErrorKindFailed, 500, 12, 500))
	twice := fmt.Errorf("queue task failed: %w", inner)

	if cls := ClassifyGenerationFailure(twice); cls.Reason != FailureReasonToolCallBudget {
		t.Fatalf("ClassifyGenerationFailure(double wrapped cap) = %q, want %q", cls.Reason, FailureReasonToolCallBudget)
	}
	metadata := map[string]interface{}{"finished": true}
	applyTerminalFailureMetadata(metadata, twice, ClassifyGenerationFailure(twice), &TurnOutcome{Reason: TurnEndedToolCallCap})
	if flag, ok := metadata["error"]; ok && flag == true {
		t.Fatalf("double-wrapped cap stop metadata error = %v, want it unset", flag)
	}
}
