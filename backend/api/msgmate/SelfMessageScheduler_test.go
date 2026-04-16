package msgmate

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseSchedulerCronRejectsMalformedExpressions(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"too few fields":   "*/5 * * *",
		"too many fields":  "0 0 */5 * * * *",
		"bad minute":       "60 * * * *",
		"bad hour":         "* 24 * * *",
		"bad dom":          "* * 0 * *",
		"bad month":        "* * * 13 *",
		"bad dow":          "* * * * 8",
		"bad step":         "*/0 * * * *",
		"non numeric step": "* * * * */x",
		"inverted range":   "* 10-5 * * *",
		"empty element":    "* 1,,2 * * *",
		"garbage":          "* * * * MONDAY",
	}
	for name, expression := range cases {
		if _, err := ParseSchedulerCron(expression); err == nil {
			t.Fatalf("%s: expected %q to be rejected", name, expression)
		}
	}
}

func TestSchedulerCronNextFiveFieldExpression(t *testing.T) {
	cron, err := ParseSchedulerCron("*/20 * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	after := time.Date(2024, 5, 1, 10, 20, 30, 0, time.UTC)
	next, ok := cron.Next(after, time.UTC)
	if !ok {
		t.Fatal("expected a next fire instant")
	}
	want := time.Date(2024, 5, 1, 10, 40, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestSchedulerCronNextSixFieldExpression(t *testing.T) {
	cron, err := ParseSchedulerCron("*/15 * * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	after := time.Date(2024, 5, 1, 10, 0, 1, 0, time.UTC)
	next, _ := cron.Next(after, time.UTC)
	if want := time.Date(2024, 5, 1, 10, 0, 15, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestSchedulerCronNextIsStrictlyAfterTheGivenInstant(t *testing.T) {
	cron, _ := ParseSchedulerCron("0 * * * *")
	exact := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	next, _ := cron.Next(exact, time.UTC)
	if !next.Equal(exact.Add(time.Hour)) {
		t.Fatalf("next = %s, want %s", next, exact.Add(time.Hour))
	}
}

func TestSchedulerCronNextNamesRangesAndDayRules(t *testing.T) {
	cron, err := ParseSchedulerCron("30 9 * JAN-MAR MON-FRI")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 2024-01-06 is a Saturday, so the first match is Monday 2024-01-08.
	next, ok := cron.Next(time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC), time.UTC)
	if !ok {
		t.Fatal("expected a next fire instant")
	}
	if want := time.Date(2024, 1, 8, 9, 30, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}

	// day-of-month and day-of-week both restricted => OR semantics.
	orCron, err := ParseSchedulerCron("0 0 1 * MON")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 2024-05-02 is a Thursday; the next match is Monday 2024-05-06.
	next, _ = orCron.Next(time.Date(2024, 5, 2, 12, 0, 0, 0, time.UTC), time.UTC)
	if want := time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("OR rule: next = %s, want %s", next, want)
	}
}

func TestSchedulerCronNextHonoursTimezone(t *testing.T) {
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	cron, err := ParseSchedulerCron("0 9 * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	next, _ := cron.Next(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), location)
	if want := time.Date(2024, 5, 1, 7, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next = %s, want %s (09:00 Berlin = 07:00 UTC in May)", next, want)
	}
}

func TestParseSelfMessageSchedules(t *testing.T) {
	entries, err := ParseSelfMessageSchedules(strings.Join([]string{
		"# the lead resumes itself every 20 minutes",
		"msgmate-coder-development-lead=*/20 * * * *",
		"",
		"night-owl=0 3 * * *@Europe/Berlin;weekly=0 9 * * MON",
	}, "\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	if entries[0].BotName != "msgmate-coder-development-lead" || entries[0].Cron != "*/20 * * * *" {
		t.Fatalf("unexpected first entry %+v", entries[0])
	}
	if entries[1].BotName != "night-owl" || entries[1].Cron != "0 3 * * *" || entries[1].Timezone != "Europe/Berlin" {
		t.Fatalf("unexpected second entry %+v", entries[1])
	}
	if entries[2].BotName != "weekly" || entries[2].Cron != "0 9 * * MON" {
		t.Fatalf("unexpected third entry %+v", entries[2])
	}
}

func TestParseSelfMessageSchedulesRejectsInvalidEntries(t *testing.T) {
	cases := []string{
		"just-a-bot-name",
		"=*/5 * * * *",
		"bot=not a cron",
		"bot=*/5 * * * *@Mars/Olympus",
	}
	for _, spec := range cases {
		if _, err := ParseSelfMessageSchedules(spec); err == nil {
			t.Fatalf("expected %q to be rejected", spec)
		}
	}
}

func TestSelfMessageSchedulesFromEnvDefaultsAndOverrides(t *testing.T) {
	entries, err := SelfMessageSchedulesFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	lead, ok := SelfMessageScheduleForBot(entries, SelfMessageDevelopmentLeadBotName)
	if !ok {
		t.Fatalf("the development lead is not configured by default: %+v", entries)
	}
	if lead.Cron != SelfMessageDevelopmentLeadCron {
		t.Fatalf("lead cron = %q, want %q", lead.Cron, SelfMessageDevelopmentLeadCron)
	}

	// An entry overrides the built-in default of the same bot and adds others.
	env := map[string]string{
		SelfMessageScheduleEnvVar: "MSGMATE-CODER-DEVELOPMENT-LEAD=*/5 * * * *;other-bot=0 * * * *",
	}
	entries, err = SelfMessageSchedulesFromEnv(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	lead, _ = SelfMessageScheduleForBot(entries, SelfMessageDevelopmentLeadBotName)
	if lead.Cron != "*/5 * * * *" {
		t.Fatalf("override not applied: %+v", lead)
	}
	if _, ok := SelfMessageScheduleForBot(entries, "other-bot"); !ok {
		t.Fatalf("extra bot schedule missing: %+v", entries)
	}

	// A malformed spec surfaces as an error instead of silently degrading.
	env[SelfMessageScheduleEnvVar] = "broken"
	if _, err := SelfMessageSchedulesFromEnv(func(key string) string { return env[key] }); err == nil {
		t.Fatal("expected a malformed schedule spec to fail")
	}
}

func selfMessageTestScheduler(t *testing.T, now time.Time) (*SelfMessageScheduler, *[]time.Time, *[]string) {
	t.Helper()
	scheduler := NewSelfMessageScheduler()
	fires := &[]time.Time{}
	cancels := &[]string{}
	scheduler.SetNow(func() time.Time { return now })
	scheduler.SetEnqueue(func(_ SelfMessageSchedule, fireAt time.Time, _ string) error {
		*fires = append(*fires, fireAt)
		return nil
	})
	scheduler.SetCancel(func(scheduleID string) error {
		*cancels = append(*cancels, scheduleID)
		return nil
	})
	return scheduler, fires, cancels
}

func TestSelfMessageSchedulerRegistersSingleActiveSchedule(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	scheduler, fires, _ := selfMessageTestScheduler(t, now)

	schedule := SelfMessageSchedule{BotUserID: 7, ChatUUID: "chat-1", Prompt: "resume", Cron: "*/20 * * * *"}
	id, err := scheduler.Schedule(schedule)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if id == "" || !strings.HasPrefix(id, selfMessageTaskPrefix) {
		t.Fatalf("unexpected schedule id %q", id)
	}
	if len(*fires) != 1 || !(*fires)[0].Equal(now.Add(20*time.Minute)) {
		t.Fatalf("unexpected initial fires %v", *fires)
	}
	if next, ok := scheduler.NextRun(schedule.Key()); !ok || !next.Equal(now.Add(20*time.Minute)) {
		t.Fatalf("next run = %s (ok=%v)", next, ok)
	}
	if scheduler.Len() != 1 {
		t.Fatalf("len = %d, want 1", scheduler.Len())
	}
}

func TestSelfMessageSchedulerSingleActiveReplacesOlderSchedule(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	scheduler, fires, cancels := selfMessageTestScheduler(t, now)
	key := SelfMessageSchedule{}.Key()

	older := SelfMessageSchedule{BotUserID: 7, ChatUUID: "chat-1", Prompt: "older", Cron: "*/20 * * * *"}
	key = older.Key()
	olderID, err := scheduler.Schedule(older)
	if err != nil {
		t.Fatalf("first schedule: %v", err)
	}

	newer := older
	newer.Prompt = "newer"
	newer.Cron = "*/5 * * * *"
	newerID, err := scheduler.Schedule(newer)
	if err != nil {
		t.Fatalf("second schedule: %v", err)
	}
	if newerID == olderID {
		t.Fatal("the replacement must get its own schedule id")
	}
	if len(*cancels) != 1 || (*cancels)[0] != olderID {
		t.Fatalf("the superseded schedule was not cancelled: %v", *cancels)
	}
	if scheduler.Len() != 1 {
		t.Fatalf("len = %d, want exactly one active schedule", scheduler.Len())
	}
	if active, ok := scheduler.Active(key); !ok || active.Prompt != "newer" {
		t.Fatalf("active schedule = %+v (ok=%v), want the newest", active, ok)
	}
	if len(*fires) != 2 || !(*fires)[1].Equal(now.Add(5*time.Minute)) {
		t.Fatalf("unexpected fires %v", *fires)
	}
}

func TestSelfMessageSchedulerDropsSupersededFire(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	scheduler, fires, _ := selfMessageTestScheduler(t, now)
	older := SelfMessageSchedule{BotUserID: 7, ChatUUID: "chat-1", Prompt: "older", Cron: "*/20 * * * *"}
	olderID, _ := scheduler.Schedule(older)
	newer := older
	newer.Cron = "*/5 * * * *"
	if _, err := scheduler.Schedule(newer); err != nil {
		t.Fatalf("second schedule: %v", err)
	}
	before := len(*fires)

	// A stale fire of the superseded schedule must be dropped and never
	// rescheduled.
	if _, applied, err := scheduler.Fire(older.Key(), olderID); err != nil || applied {
		t.Fatalf("stale fire: applied=%v err=%v, want dropped", applied, err)
	}
	if len(*fires) != before {
		t.Fatalf("a stale fire was rescheduled: %v", *fires)
	}

	// The winning schedule reschedules itself on every fire.
	active, ok := scheduler.Active(older.Key())
	if !ok {
		t.Fatal("expected an active schedule")
	}
	id, _ := scheduler.Schedule(active)
	applied, err := scheduler.Fire(active.Key(), id)
	if err != nil || !applied {
		t.Fatalf("active fire: applied=%v err=%v, want applied", applied, err)
	}
	if len(*fires) != before+2 {
		t.Fatalf("the active schedule did not reschedule: %v", *fires)
	}
	if next, _ := scheduler.NextRun(active.Key()); !next.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("next run = %s, want %s", next, now.Add(5*time.Minute))
	}
}

func TestSelfMessageSchedulerCancelAndValidation(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	scheduler, _, cancels := selfMessageTestScheduler(t, now)

	invalid := []SelfMessageSchedule{
		{ChatUUID: "chat-1", Prompt: "x", Cron: "* * * * *"},
		{BotUserID: 1, Prompt: "x", Cron: "* * * * *"},
		{BotUserID: 1, ChatUUID: "chat-1", Cron: "* * * * *"},
		{BotUserID: 1, ChatUUID: "chat-1", Prompt: "x", Cron: "nope"},
		{BotUserID: 1, ChatUUID: "chat-1", Prompt: "x", Cron: "* * * * *", Timezone: "Mars/Olympus"},
	}
	for _, schedule := range invalid {
		if _, err := scheduler.Schedule(schedule); err == nil {
			t.Fatalf("expected %+v to be rejected", schedule)
		}
	}
	if scheduler.Len() != 0 {
		t.Fatalf("a rejected schedule must not become active (len=%d)", scheduler.Len())
	}

	valid := SelfMessageSchedule{BotUserID: 1, ChatUUID: "chat-1", Prompt: "x", Cron: "* * * * *"}
	id, err := scheduler.Schedule(valid)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if !scheduler.Cancel(valid.Key()) {
		t.Fatal("cancel of an active schedule must report true")
	}
	if len(*cancels) != 1 || (*cancels)[0] != id {
		t.Fatalf("unexpected cancels %v", *cancels)
	}
	if scheduler.Cancel(valid.Key()) {
		t.Fatal("cancel of an inactive schedule must report false")
	}
	if scheduler.Len() != 0 {
		t.Fatalf("len = %d, want 0", scheduler.Len())
	}
}

func TestSelfMessageSchedulerEnqueueFailureKeepsOlderScheduleCancelled(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	scheduler, _, cancels := selfMessageTestScheduler(t, now)
	older := SelfMessageSchedule{BotUserID: 9, ChatUUID: "chat-1", Prompt: "older", Cron: "*/20 * * * *"}
	if _, err := scheduler.Schedule(older); err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	scheduler.SetEnqueue(func(SelfMessageSchedule, time.Time, string) error {
		return errors.New("queue unavailable")
	})
	if _, err := scheduler.Schedule(older); err == nil {
		t.Fatal("expected the enqueue failure to surface")
	}
	if len(*cancels) != 1 {
		t.Fatalf("the older schedule should still be cancelled: %v", *cancels)
	}
	if scheduler.Len() != 0 {
		t.Fatalf("len = %d, want 0 after a failed registration", scheduler.Len())
	}
}

func TestSelfMessageSchedulerIsSafeForConcurrentScheduling(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	scheduler, _, _ := selfMessageTestScheduler(t, now)
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			schedule := SelfMessageSchedule{
				BotUserID: 3,
				ChatUUID:  "chat-1",
				Prompt:    "resume",
				Cron:      "*/5 * * * *",
			}
			if _, err := scheduler.Schedule(schedule); err != nil {
				t.Errorf("concurrent schedule: %v", err)
			}
		}(worker)
	}
	waitGroup.Wait()
	if scheduler.Len() != 1 {
		t.Fatalf("len = %d, want exactly one active schedule per bot", scheduler.Len())
	}
}
