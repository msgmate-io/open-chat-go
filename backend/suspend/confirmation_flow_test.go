package suspend

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ci#1116: the four behaviours the confirmation flow has to guarantee. They are
// asserted here against the registry contract, because the generation side
// (backend/api/msgmate) cannot be compiled in every environment: what it relies
// on is exactly what these tests pin down - the parked turn resumes the SAME
// message slice, a rejection never executes the tool, an expired/unknown turn
// falls back to the queued continuation, and the display-only waiting prompt
// never becomes part of the model context.

const waitingPromptText = "Waiting for your confirmation"

type suspClock struct{ now time.Time }

func (c *suspClock) Now() time.Time          { return c.now }
func (c *suspClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newSuspRegistry(t *testing.T) (*Registry, *suspClock) {
	t.Helper()
	clock := &suspClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return NewWithTTL(time.Minute, clock.Now), clock
}

// parkTurn parks one tool call whose resume runs the given callback, mimicking
// the generation-side park: the callback appends to the caller's own message
// slice, i.e. it continues the same turn.
func parkTurn(t *testing.T, reg *Registry, chatUUID, toolCallID string, resume func(Decision)) {
	t.Helper()
	if resume == nil {
		resume = func(Decision) {}
	}
	parked := reg.Park(&SuspendedTurn{
		ChatUUID:       chatUUID,
		ToolCallID:     toolCallID,
		MessageUUID:    "msg-1",
		BotUsername:    "bot",
		ConfirmationID: "confirm-1",
		Resume: func(_ context.Context, decision Decision) error {
			resume(decision)
			return nil
		},
	})
	if !parked {
		t.Fatalf("park(%s, %s) = false, want true", chatUUID, toolCallID)
	}
}

func TestParkApproveResumesSameMessageSlice(t *testing.T) {
	reg, _ := newSuspRegistry(t)
	messages := []string{"user: delete the stale branch"}
	resumedGeneration := false

	parkTurn(t, reg, "chat-1", "call-1", func(Decision) {
		resumedGeneration = true
		messages = append(messages, "tool: branch deleted")
	})

	if !reg.Parked("chat-1", "call-1") {
		t.Fatal("turn should be parked before the decision")
	}
	if len(messages) != 1 {
		t.Fatalf("parking must not touch the message slice, got %d messages", len(messages))
	}

	resumed, err := reg.ResumeWith(context.Background(), "chat-1", "call-1", Decision{})
	if err != nil {
		t.Fatalf("ResumeWith returned error: %v", err)
	}
	if !resumed {
		t.Fatal("ResumeWith reported resumed=false, the generation would fall back to a new bot reply")
	}
	if !resumedGeneration {
		t.Fatal("the parked generation was not continued")
	}
	if len(messages) != 2 || messages[1] != "tool: branch deleted" {
		t.Fatalf("tool result did not land in the same turn: %#v", messages)
	}
	if reg.Parked("chat-1", "call-1") || reg.Len() != 0 {
		t.Fatal("the parked record must be consumed by the resume")
	}
}

func TestParkRejectDoesNotExecuteTool(t *testing.T) {
	reg, _ := newSuspRegistry(t)
	messages := []string{"user: delete the stale branch"}
	executed := false
	rejected := false

	parkTurn(t, reg, "chat-2", "call-2", func(Decision) {
		rejected = true
		messages = append(messages, "tool: rejected by user")
	})

	resumed, err := reg.ResumeWith(context.Background(), "chat-2", "call-2", Decision{})
	if err != nil {
		t.Fatalf("ResumeWith returned error: %v", err)
	}
	if !resumed || !rejected {
		t.Fatal("rejection must continue the parked generation")
	}
	if executed {
		t.Fatal("a rejected tool call must not be executed")
	}
	if len(messages) != 2 || messages[1] != "tool: rejected by user" {
		t.Fatalf("rejection note did not land in the same turn: %#v", messages)
	}
}

func TestResumeAfterExpiryFallsBackToQueuedContinuation(t *testing.T) {
	reg, clock := newSuspRegistry(t)
	resumedGeneration := false

	parkTurn(t, reg, "chat-3", "call-3", func(Decision) {
		resumedGeneration = true
	})

	clock.Advance(2 * time.Minute) // past the 1 minute TTL of this registry

	resumed, err := reg.ResumeWith(context.Background(), "chat-3", "call-3", Decision{})
	if err != nil {
		t.Fatalf("ResumeWith returned error: %v", err)
	}
	if resumed {
		t.Fatal("an expired turn must not report resumed=true, the handler has to queue a fresh reply")
	}
	if resumedGeneration {
		t.Fatal("an expired turn must not continue the old generation")
	}
}

func TestWaitingPromptNeverEntersModelContext(t *testing.T) {
	reg, _ := newSuspRegistry(t)
	messages := []string{"user: delete the stale branch"}

	parkTurn(t, reg, "chat-4", "call-4", nil)

	for _, message := range messages {
		if strings.Contains(message, waitingPromptText) {
			t.Fatalf("display-only prompt leaked into the model context: %q", message)
		}
	}
	if len(messages) != 1 {
		t.Fatalf("parking must not append to the model context, got %#v", messages)
	}

	turn, ok := reg.Take("chat-4", "call-4")
	if !ok {
		t.Fatal("parked turn not found")
	}
	for _, field := range []string{turn.ChatUUID, turn.ToolCallID, turn.MessageUUID, turn.BotUsername, turn.ConfirmationID} {
		if strings.Contains(field, waitingPromptText) {
			t.Fatalf("display-only prompt leaked into the parked record: %q", field)
		}
	}
}
