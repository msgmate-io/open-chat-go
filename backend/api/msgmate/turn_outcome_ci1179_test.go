package msgmate

import (
	"errors"
	"testing"

	"github.com/msgmate-io/open-chat-go/backend/api/chats/chatstate"
)

// ci#1179: provider admission back-pressure is a capacity stop OpenChat
// performed by design, so it must not be flagged with `error: true` on the
// finished message.
func TestFailureReasonExemptFromErrorFlagCi1179(t *testing.T) {
	exempt := []string{FailureReasonToolCallBudget, FailureReasonProviderBackpressure}
	for _, reason := range exempt {
		if !failureReasonExemptFromErrorFlag(reason) {
			t.Fatalf("expected reason %q to be exempt from the error flag", reason)
		}
	}
	notExempt := []string{FailureReasonInternal, FailureReasonProviderError, FailureReasonTimeout, ""}
	for _, reason := range notExempt {
		if failureReasonExemptFromErrorFlag(reason) {
			t.Fatalf("expected reason %q NOT to be exempt from the error flag", reason)
		}
	}
}

// ci#1179: the exemption is what keeps a back-pressured turn out of the error
// surface; every other terminal failure keeps the historic behaviour.
func TestApplyTerminalFailureMetadataCi1179(t *testing.T) {
	streamErr := errors.New("provider openrouter: saturated")

	t.Run("provider backpressure stays exempt", func(t *testing.T) {
		metadata := map[string]interface{}{}
		outcome := &TurnOutcome{Reason: TurnEndedProviderError}
		applyTerminalFailureMetadata(metadata, streamErr, GenerationFailureClassification{Reason: FailureReasonProviderBackpressure}, outcome)

		if _, ok := metadata["error"]; ok {
			t.Fatalf("provider backpressure must not set the error flag, got %v", metadata["error"])
		}
		if _, ok := metadata["error_detail"]; ok {
			t.Fatalf("provider backpressure must not set error_detail, got %v", metadata["error_detail"])
		}
		if got, _ := metadata["failure_reason"].(string); got != FailureReasonProviderBackpressure {
			t.Fatalf("expected failure_reason=%q, got %q", FailureReasonProviderBackpressure, got)
		}
		if got, _ := metadata["turn_ended"].(string); got != TurnEndedProviderError {
			t.Fatalf("expected turn_ended=%q, got %q", TurnEndedProviderError, got)
		}
	})

	t.Run("internal failure still flags an error", func(t *testing.T) {
		metadata := map[string]interface{}{}
		outcome := &TurnOutcome{Reason: TurnEndedProviderError}
		applyTerminalFailureMetadata(metadata, streamErr, GenerationFailureClassification{Reason: FailureReasonInternal}, outcome)

		if flagged, _ := metadata["error"].(bool); !flagged {
			t.Fatalf("expected the error flag for an internal failure, got %v", metadata["error"])
		}
		if _, ok := metadata["turn_ended"]; ok {
			t.Fatal("a genuine failure must not report a resumable turn_ended")
		}
	})
}

// resolveFinishedMessageState mirrors the finished-message branch of
// chats.InteractionStatus (backend/api/chats/InteractionStatus.go ~533-541): a
// finished bot message that also carries `error: true` resolves StateFailed,
// otherwise StateFinished. It is duplicated here (instead of exported) because
// ci#1179 deliberately does not change that file: keeping `error` off the
// finished message is what stops the interaction resolving StateFailed.
func resolveFinishedMessageState(metadata map[string]interface{}) chatstate.State {
	if errFlag, _ := metadata["error"].(bool); errFlag {
		return chatstate.StateFailed
	}
	return chatstate.StateFinished
}

// ci#1179 step 4: pin the both-ways contract the exemption relies on - with the
// flag off the interaction resolves StateFinished, which is what makes the
// scheduled recovery visible instead of looking like a defect.
func TestFinishedMessageErrorFlagResolvesStateCi1179(t *testing.T) {
	cases := []struct {
		name      string
		flagged   bool
		wantState chatstate.State
	}{
		{name: "no error flag -> finished", flagged: false, wantState: chatstate.StateFinished},
		{name: "error flag -> failed", flagged: true, wantState: chatstate.StateFailed},
	}
	for _, tc := range cases {
		metadata := map[string]interface{}{"finished": true}
		if tc.flagged {
			metadata["error"] = true
		}
		if state := resolveFinishedMessageState(metadata); state != tc.wantState {
			t.Fatalf("%s: expected state %q, got %q", tc.name, tc.wantState, state)
		}
	}

	// End to end for the exemption: the metadata applyTerminalFailureMetadata
	// writes for a back-pressure stop must resolve StateFinished, while the
	// metadata of a genuine failure resolves StateFailed.
	backpressure := map[string]interface{}{"finished": true}
	applyTerminalFailureMetadata(backpressure, errors.New("saturated"),
		GenerationFailureClassification{Reason: FailureReasonProviderBackpressure},
		&TurnOutcome{Reason: TurnEndedProviderError})
	if state := resolveFinishedMessageState(backpressure); state != chatstate.StateFinished {
		t.Fatalf("a back-pressure stop must resolve %q, got %q", chatstate.StateFinished, state)
	}

	failure := map[string]interface{}{"finished": true}
	applyTerminalFailureMetadata(failure, errors.New("boom"),
		GenerationFailureClassification{Reason: FailureReasonInternal},
		&TurnOutcome{Reason: TurnEndedProviderError})
	if state := resolveFinishedMessageState(failure); state != chatstate.StateFailed {
		t.Fatalf("a genuine failure must resolve %q, got %q", chatstate.StateFailed, state)
	}
}
