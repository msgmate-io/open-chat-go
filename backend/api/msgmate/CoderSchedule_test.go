package msgmate

import (
	"strings"
	"testing"

	"backend/database"
)

// ci#724: the runner owns task IDs of the form
// "coder-schedule:<chatUUID>:<nonce>[:<correlationID>]" and refuses anything
// else, so `coder_schedule(cancel_id=...)` can never delete an unrelated queue
// task (in particular not the chat-wide reply task of a live turn).

func TestCoderScheduleTaskIDCarriesChatAndNonce(t *testing.T) {
	first := coderScheduleTaskID("chat-1", "")
	second := coderScheduleTaskID("chat-1", "")
	if !strings.HasPrefix(first, coderScheduleTaskPrefix+"chat-1:") {
		t.Fatalf("task id %q does not carry the chat uuid", first)
	}
	if first == second {
		t.Fatalf("two schedules of one chat collided on %q", first)
	}

	withCorrelation := coderScheduleTaskID("chat-1", "  corr  ")
	if !strings.HasSuffix(withCorrelation, ":corr") {
		t.Fatalf("task id %q does not carry the trimmed correlation id", withCorrelation)
	}
	if !strings.HasPrefix(withCorrelation, coderScheduleTaskPrefix+"chat-1:") {
		t.Fatalf("task id %q lost the chat uuid prefix", withCorrelation)
	}
}

func TestCancelCoderTurnRefusesForeignTaskIDs(t *testing.T) {
	runner := coderScheduleRunner{}
	for _, foreign := range []string{
		"bot-reply:chat-1",                        // the chat-wide reply task of a live turn
		"coder-schedule:chat-2:abc",               // another chat's scheduled turn
		"coder-schedule:chat-1x:abc",              // a chat uuid that merely starts alike
		"asynq:{default}:t:bot-reply:chat-1",      // a raw asynq task key
		strings.Repeat("coder-schedule:", 1) + "", // empty id after the prefix
	} {
		cancelled, err := runner.CancelCoderTurn("chat-1", foreign)
		if err == nil {
			t.Fatalf("CancelCoderTurn(%q) was accepted; it must be refused", foreign)
		}
		if cancelled {
			t.Fatalf("CancelCoderTurn(%q) reported a cancellation", foreign)
		}
	}
}

func TestCancelCoderTurnValidatesInput(t *testing.T) {
	runner := coderScheduleRunner{}
	if _, err := runner.CancelCoderTurn("", "coder-schedule:chat-1:abc"); err == nil {
		t.Fatal("a missing chat uuid must be refused")
	}
	if _, err := runner.CancelCoderTurn("chat-1", "   "); err == nil {
		t.Fatal("a missing schedule id must be refused")
	}
}

func TestScheduleCoderTurnValidatesInput(t *testing.T) {
	runner := coderScheduleRunner{}
	if _, err := runner.ScheduleCoderTurn("", "pulse", 60, ""); err == nil {
		t.Fatal("a missing chat uuid must be refused")
	}
	if _, err := runner.ScheduleCoderTurn("chat-1", "   ", 60, ""); err == nil {
		t.Fatal("a missing prompt must be refused")
	}
	if _, err := runner.ScheduleCoderTurn("chat-1", "pulse", 0, ""); err == nil {
		t.Fatal("a non-positive delay must be refused")
	}
}

func TestCoderScheduleParticipantsSplitsAutomatedSide(t *testing.T) {
	bot := database.User{IsAutomated: true}
	human := database.User{IsAutomated: false}

	botSide, userSide := coderScheduleParticipants(database.Chat{User1: human, User2: bot})
	if botSide == nil || userSide == nil || !botSide.IsAutomated || userSide.IsAutomated {
		t.Fatalf("participants not split: bot=%v user=%v", botSide, userSide)
	}

	botSide, userSide = coderScheduleParticipants(database.Chat{User1: bot, User2: human})
	if botSide == nil || userSide == nil || !botSide.IsAutomated || userSide.IsAutomated {
		t.Fatalf("participants not split in the other order: bot=%v user=%v", botSide, userSide)
	}

	// A chat without exactly one automated side has no schedulable turn.
	if botSide, userSide := coderScheduleParticipants(database.Chat{User1: bot, User2: bot}); botSide != nil || userSide != nil {
		t.Fatal("a chat of two bots must not be schedulable")
	}
	if botSide, userSide := coderScheduleParticipants(database.Chat{User1: human, User2: human}); botSide != nil || userSide != nil {
		t.Fatal("a chat without a bot must not be schedulable")
	}
}
