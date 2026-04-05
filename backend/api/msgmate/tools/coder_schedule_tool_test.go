package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

type fakeCoderScheduleRunner struct {
	chatUUID      string
	prompt        string
	delay         int
	id            string
	scheduleID    string
	scheduleCalls int
	scheduleErr   error
	cancelUUID    string
	cancelID      string
	cancelled     bool
	cancelErr     error
}

func (f *fakeCoderScheduleRunner) ScheduleCoderTurn(chatUUID string, prompt string, delaySeconds int, id string) (string, error) {
	f.scheduleCalls++
	f.chatUUID, f.prompt, f.delay, f.id = chatUUID, prompt, delaySeconds, id
	if f.scheduleErr != nil {
		return "", f.scheduleErr
	}
	return f.scheduleID, nil
}

func (f *fakeCoderScheduleRunner) CancelCoderTurn(chatUUID string, scheduleID string) (bool, error) {
	f.cancelUUID, f.cancelID = chatUUID, scheduleID
	return f.cancelled, f.cancelErr
}

func decodeCoderScheduleResult(t *testing.T, raw string) coderScheduleResult {
	t.Helper()
	var result coderScheduleResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("result is not valid JSON (%q): %v", raw, err)
	}
	return result
}

func TestClampCoderScheduleDelay(t *testing.T) {
	cases := []struct{ in, want int }{{-5, 60}, {0, 60}, {60, 60}, {600, 600}, {3600, 3600}, {3601, 3600}}
	for _, tc := range cases {
		if got := ClampCoderScheduleDelay(tc.in); got != tc.want {
			t.Fatalf("ClampCoderScheduleDelay(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestCoderScheduleSchedulesOnTheCallersChat(t *testing.T) {
	runner := &fakeCoderScheduleRunner{scheduleID: "task-1"}
	var input CoderScheduleInput
	if err := json.Unmarshal([]byte(`{"delay_seconds": 600, "prompt": "  pulse: keep going  ", "id": "pulse-1"}`), &input); err != nil {
		t.Fatalf("input must decode from the schema fields: %v", err)
	}
	raw, err := runCoderSchedule(input, map[string]interface{}{"_runtime": map[string]interface{}{"chat_uuid": "chat-9"}}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.chatUUID != "chat-9" {
		t.Fatalf("the schedule must target the trusted chat of the turn, got %q", runner.chatUUID)
	}
	if runner.prompt != "pulse: keep going" {
		t.Fatalf("prompt must be trimmed, got %q", runner.prompt)
	}
	if runner.delay != 600 {
		t.Fatalf("delay must pass through, got %d", runner.delay)
	}
	if runner.id != "pulse-1" {
		t.Fatalf("correlation id must pass through, got %q", runner.id)
	}
	result := decodeCoderScheduleResult(t, raw)
	if !result.Scheduled || result.ScheduleID != "task-1" || result.ChatUUID != "chat-9" || result.DelaySeconds != 600 || result.ID != "pulse-1" {
		t.Fatalf("unexpected result: %s", raw)
	}
	if !strings.Contains(result.Note, "cancel_id=\"task-1\"") {
		t.Fatalf("the result must tell the model how to cancel: %s", raw)
	}
}

func TestCoderScheduleClampsTheDelayEndToEnd(t *testing.T) {
	runner := &fakeCoderScheduleRunner{scheduleID: "task-1"}
	raw, err := runCoderSchedule(CoderScheduleInput{Prompt: "pulse"}, map[string]interface{}{"chat_uuid": "chat-1"}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.delay != CoderScheduleMinDelaySeconds {
		t.Fatalf("an unset delay must clamp to the minimum, got %d", runner.delay)
	}
	if result := decodeCoderScheduleResult(t, raw); result.DelaySeconds != CoderScheduleMinDelaySeconds {
		t.Fatalf("the result must report the clamped delay: %s", raw)
	}
}

func TestCoderScheduleRejectsMissingInputAndExclusivity(t *testing.T) {
	runner := &fakeCoderScheduleRunner{scheduleID: "task-1"}
	init := map[string]interface{}{"chat_uuid": "chat-1"}
	if _, err := runCoderSchedule(CoderScheduleInput{}, init, runner); err == nil {
		t.Fatal("an empty input must be rejected")
	}
	if _, err := runCoderSchedule(CoderScheduleInput{Prompt: "pulse", CancelID: "task-1"}, init, runner); err == nil {
		t.Fatal("scheduling and cancelling at once must be rejected")
	}
	if _, err := runCoderSchedule(CoderScheduleInput{CancelID: "task-1"}, map[string]interface{}{}, runner); err == nil {
		t.Fatal("a cancel without the trusted chat identity must be rejected")
	}
	if runner.scheduleCalls != 0 {
		t.Fatalf("the runner must not be called for invalid input, got %d calls", runner.scheduleCalls)
	}
}

func TestCoderScheduleCancelTargetsOnlyThatSchedule(t *testing.T) {
	runner := &fakeCoderScheduleRunner{cancelled: true}
	raw, err := runCoderSchedule(CoderScheduleInput{CancelID: " task-7 "}, map[string]interface{}{"chat_uuid": "chat-1"}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.scheduleCalls != 0 {
		t.Fatalf("a cancel must never schedule a new turn, got %d schedule calls", runner.scheduleCalls)
	}
	if runner.cancelUUID != "chat-1" || runner.cancelID != "task-7" {
		t.Fatalf("cancel must target the returned id, got chat=%q id=%q", runner.cancelUUID, runner.cancelID)
	}
	result := decodeCoderScheduleResult(t, raw)
	if result.Scheduled || !result.Cancelled || result.ScheduleID != "task-7" {
		t.Fatalf("unexpected cancel result: %s", raw)
	}
}

func TestCoderScheduleReporterIsMissingIsLoud(t *testing.T) {
	if _, err := runCoderSchedule(CoderScheduleInput{Prompt: "pulse"}, map[string]interface{}{"chat_uuid": "chat-1"}, nil); err == nil {
		t.Fatal("a missing runner must fail loudly instead of dropping the schedule")
	}
	if CoderScheduleRunnerProvider != nil {
		t.Fatal("the tool must not assume a runner provider was published")
	}
}
