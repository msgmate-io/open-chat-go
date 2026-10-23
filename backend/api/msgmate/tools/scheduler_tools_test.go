package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ci#744: pins the cron self-messaging tool surface (scheduler_create,
// scheduler_list, scheduler_cancel).

type fakeSchedulerRunner struct {
	created   []SchedulerSelfMessageRequest
	statuses  []SchedulerSelfMessageStatus
	cancelled []string
	cancelAll bool
	lastChat  string
	lastBot   string
	err       error
}

func (f *fakeSchedulerRunner) CreateSchedulerSelfMessage(request SchedulerSelfMessageRequest) (SchedulerSelfMessageStatus, error) {
	if f.err != nil {
		return SchedulerSelfMessageStatus{}, f.err
	}
	f.created = append(f.created, request)
	f.lastChat = request.ChatUUID
	f.lastBot = request.BotUUID
	return SchedulerSelfMessageStatus{
		Key:           request.Key,
		ChatUUID:      request.ChatUUID,
		BotUUID:       request.BotUUID,
		Cron:          request.Cron,
		Timezone:      request.Timezone,
		State:         "active",
		NextRun:       "2024-01-01T00:00:00Z",
		MaxRuns:       request.MaxRuns,
		JitterSeconds: request.JitterSeconds,
	}, nil
}

func (f *fakeSchedulerRunner) ListSchedulerSelfMessages(chatUUID string, botUUID string) ([]SchedulerSelfMessageStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastChat = chatUUID
	f.lastBot = botUUID
	return f.statuses, nil
}

func (f *fakeSchedulerRunner) CancelSchedulerSelfMessage(chatUUID string, botUUID string, key string, all bool) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastChat = chatUUID
	f.lastBot = botUUID
	f.cancelAll = all
	if all {
		return []string{"self", "other"}, nil
	}
	f.cancelled = append(f.cancelled, key)
	return []string{key}, nil
}

func schedulerInit() map[string]interface{} {
	return map[string]interface{}{"chat_uuid": "chat-1", "bot_uuid": "bot-1"}
}

func TestSchedulerCreateDefaultsToSelfAndClamps(t *testing.T) {
	runner := &fakeSchedulerRunner{}
	out, err := runSchedulerCreate(SchedulerCreateInput{
		Cron:          "*/20 * * * *",
		Prompt:        "continue",
		DelaySeconds:  -5,
		MaxRuns:       5000,
		JitterSeconds: 9999,
		ID:            "corr-1",
	}, schedulerInit(), runner)
	if err != nil {
		t.Fatalf("scheduler_create failed: %v", err)
	}
	if len(runner.created) != 1 {
		t.Fatalf("expected one schedule, got %d", len(runner.created))
	}
	request := runner.created[0]
	if request.Target != "self" {
		t.Fatalf("target = %q, want self", request.Target)
	}
	if request.Key != SchedulerDefaultKey {
		t.Fatalf("key = %q, want %q", request.Key, SchedulerDefaultKey)
	}
	if request.ChatUUID != "chat-1" || request.BotUUID != "bot-1" {
		t.Fatalf("schedule lost the trusted identity: %+v", request)
	}
	if request.DelaySeconds != 0 {
		t.Fatalf("delay = %d, want clamped 0", request.DelaySeconds)
	}
	if request.MaxRuns != SchedulerMaxRunsCap {
		t.Fatalf("max_runs = %d, want clamped %d", request.MaxRuns, SchedulerMaxRunsCap)
	}
	if request.JitterSeconds != SchedulerMaxJitterSeconds {
		t.Fatalf("jitter = %d, want clamped %d", request.JitterSeconds, SchedulerMaxJitterSeconds)
	}
	if request.CorrelationID != "corr-1" {
		t.Fatalf("correlation id = %q, want corr-1", request.CorrelationID)
	}
	var result schedulerToolResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("scheduler_create result is not JSON: %v (%s)", err, out)
	}
	if !result.Created || result.Schedule == nil || result.Schedule.Key != SchedulerDefaultKey {
		t.Fatalf("unexpected scheduler_create result: %s", out)
	}
	if !strings.Contains(result.Note, "superseded") {
		t.Fatalf("the note must state the single-active supersede, got %q", result.Note)
	}
}

func TestSchedulerCreateRejectsForeignTargetsAndBadInput(t *testing.T) {
	runner := &fakeSchedulerRunner{}
	cases := []struct {
		name  string
		input SchedulerCreateInput
	}{
		{"no cron", SchedulerCreateInput{Prompt: "go"}},
		{"bad cron", SchedulerCreateInput{Cron: "*/20 * * *", Prompt: "go"}},
		{"no prompt", SchedulerCreateInput{Cron: "*/20 * * * *"}},
		{"other target", SchedulerCreateInput{Cron: "*/20 * * * *", Prompt: "go", Target: "other-bot"}},
		{"unknown timezone", SchedulerCreateInput{Cron: "*/20 * * * *", Prompt: "go", Timezone: "Mars/Olympus"}},
		{"negative max runs", SchedulerCreateInput{Cron: "*/20 * * * *", Prompt: "go", MaxRuns: -1}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := runSchedulerCreate(testCase.input, schedulerInit(), runner); err == nil {
				t.Fatalf("%s was accepted", testCase.name)
			}
		})
	}
	if len(runner.created) != 0 {
		t.Fatalf("rejected input still reached the runner: %+v", runner.created)
	}
	// A schedule always needs the trusted chat of the running turn.
	if _, err := runSchedulerCreate(SchedulerCreateInput{Cron: "*/20 * * * *", Prompt: "go"}, map[string]interface{}{}, runner); err == nil {
		t.Fatal("scheduler_create accepted a turn without a chat identity")
	}
	// A nil runner must fail loudly instead of dropping the schedule.
	if _, err := runSchedulerCreate(SchedulerCreateInput{Cron: "*/20 * * * *", Prompt: "go"}, schedulerInit(), nil); err == nil {
		t.Fatal("scheduler_create silently accepted an unavailable runner")
	}
}

func TestSchedulerListAndCancel(t *testing.T) {
	runner := &fakeSchedulerRunner{statuses: []SchedulerSelfMessageStatus{
		{Key: "self", ChatUUID: "chat-1", State: "active", RunsUsed: 3, MaxRuns: 10, LastSkipReason: "turn_live_budget"},
		{Key: "other", ChatUUID: "chat-1", State: "active"},
	}}
	out, err := runSchedulerList(SchedulerListInput{}, schedulerInit(), runner)
	if err != nil {
		t.Fatalf("scheduler_list failed: %v", err)
	}
	var listed schedulerToolResult
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("scheduler_list result is not JSON: %v (%s)", err, out)
	}
	if len(listed.Schedules) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(listed.Schedules))
	}
	if listed.Schedules[0].LastSkipReason != "turn_live_budget" || listed.Schedules[0].RunsUsed != 3 {
		t.Fatalf("scheduler_list must expose skip reason and runs used: %s", out)
	}
	if runner.lastChat != "chat-1" || runner.lastBot != "bot-1" {
		t.Fatalf("list resolved the wrong identity: %q/%q", runner.lastChat, runner.lastBot)
	}

	out, err = runSchedulerList(SchedulerListInput{Key: "self"}, schedulerInit(), runner)
	if err != nil {
		t.Fatalf("filtered scheduler_list failed: %v", err)
	}
	listed = schedulerToolResult{}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("filtered scheduler_list result is not JSON: %v", err)
	}
	if len(listed.Schedules) != 1 || listed.Schedules[0].Key != "self" {
		t.Fatalf("key filter did not narrow the list: %s", out)
	}

	// Cancel without a key cancels the single-active default schedule.
	if _, err := runSchedulerCancel(SchedulerCancelInput{}, schedulerInit(), runner); err != nil {
		t.Fatalf("scheduler_cancel failed: %v", err)
	}
	if len(runner.cancelled) != 1 || runner.cancelled[0] != SchedulerDefaultKey {
		t.Fatalf("cancel did not target the default key: %+v", runner.cancelled)
	}
	// Cancel all.
	out, err = runSchedulerCancel(SchedulerCancelInput{All: true}, schedulerInit(), runner)
	if err != nil {
		t.Fatalf("scheduler_cancel(all) failed: %v", err)
	}
	if !runner.cancelAll {
		t.Fatal("cancel(all=true) did not propagate")
	}
	cancelled := schedulerToolResult{}
	if err := json.Unmarshal([]byte(out), &cancelled); err != nil {
		t.Fatalf("scheduler_cancel result is not JSON: %v", err)
	}
	if len(cancelled.CancelledKeys) != 2 || !cancelled.Cancelled {
		t.Fatalf("unexpected cancel result: %s", out)
	}
}

func TestSchedulerToolsNeedTheTrustedIdentity(t *testing.T) {
	runner := &fakeSchedulerRunner{}
	if _, err := runSchedulerList(SchedulerListInput{}, map[string]interface{}{}, runner); err == nil {
		t.Fatal("scheduler_list accepted a turn without a chat identity")
	}
	if _, err := runSchedulerCancel(SchedulerCancelInput{}, map[string]interface{}{}, runner); err == nil {
		t.Fatal("scheduler_cancel accepted a turn without a chat identity")
	}
}

func TestSchedulerToolDefsAreWired(t *testing.T) {
	defs := []ToolDefinition{SchedulerCreateToolDef, SchedulerListToolDef, SchedulerCancelToolDef}
	names := []string{SchedulerCreateToolName, SchedulerListToolName, SchedulerCancelToolName}
	for index, def := range defs {
		if def.Name != names[index] {
			t.Fatalf("tool %d is named %q, want %q", index, def.Name, names[index])
		}
		if !def.RequiresInit {
			t.Fatalf("%s must receive the trusted runtime init to resolve the chat", def.Name)
		}
	}
}

func TestParseSchedulerCronExpr(t *testing.T) {
	expr, err := ParseSchedulerCronExpr("*/15 * * * *")
	if err != nil {
		t.Fatalf("5-field cron rejected: %v", err)
	}
	if !containsSchedulerValue(expr.minutes, 0) || !containsSchedulerValue(expr.minutes, 15) || containsSchedulerValue(expr.minutes, 7) {
		t.Fatalf("*/15 expanded to %v", expr.minutes)
	}
	if _, err := ParseSchedulerCronExpr("0 */30 * * * *"); err != nil {
		t.Fatalf("6-field cron rejected: %v", err)
	}
	for _, bad := range []string{"", "* * * *", "*/0 * * * *", "61 * * * *", "* * * * 9"} {
		if _, err := ParseSchedulerCronExpr(bad); err == nil {
			t.Fatalf("cron %q was accepted", bad)
		}
	}
	// Sunday is accepted as both 0 and 7.
	expr, err = ParseSchedulerCronExpr("0 9 * * 7")
	if err != nil {
		t.Fatalf("sunday=7 rejected: %v", err)
	}
	if !containsSchedulerValue(expr.weekdays, 0) {
		t.Fatalf("sunday=7 normalized to %v", expr.weekdays)
	}
	if _, ok := expr.Next(timeZero()); !ok {
		t.Fatal("a valid expression must have a next run")
	}
}

func timeZero() (zero time.Time) { return }

func TestSchedulerCronNextHonoursWeekdays(t *testing.T) {
	expr, err := ParseSchedulerCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("weekday cron rejected: %v", err)
	}
	from, parseErr := time.Parse(time.RFC3339, "2024-03-04T10:00:00Z") // Monday 10:00 UTC
	if parseErr != nil {
		t.Fatalf("bad test fixture: %v", parseErr)
	}
	next, ok := expr.Next(from)
	if !ok {
		t.Fatal("no next run for a weekday cron")
	}
	if next.Weekday() == 0 || next.Weekday() == 6 {
		t.Fatalf("weekday cron fired on %s", next.Weekday())
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Fatalf("next run = %s, want 09:00", next)
	}
}
