package msgmate

import (
	"testing"
	"time"
)

// selfMessageCoordinationRuntime builds a runtime whose schedules are injected
// directly (no database, no queue) so the ci#845 owner-key gate of the
// self-message tick path can be observed through the enqueue counter. The queue
// seam is deliberately installed only after the schedules exist, because
// scheduling itself enqueues once.
func selfMessageCoordinationRuntime(t *testing.T, now *time.Time) (*SelfMessageRuntime, *SelfMessageScheduler) {
	t.Helper()
	scheduler := NewSelfMessageScheduler()
	scheduler.SetNow(func() time.Time { return *now })
	scheduler.SetCancel(func(string) error { return nil })
	// Scheduling a cron enqueues its first run, so a no-op seam has to exist
	// before the schedules are injected; the tests replace it afterwards to
	// count the runs that the tick path actually delivers.
	scheduler.SetEnqueue(func(SelfMessageSchedule, time.Time, string) error { return nil })
	runtime := newSelfMessageRuntime(nil, nil, nil, scheduler, nil, time.Minute, func() time.Time { return *now })
	return runtime, scheduler
}

// selfMessageCoordinationInject registers a schedule in both the scheduler and
// the runtime without going through the database-backed reconcile.
func (r *SelfMessageRuntime) selfMessageCoordinationInject(t *testing.T, scheduler *SelfMessageScheduler, schedule SelfMessageSchedule) {
	t.Helper()
	scheduleID, err := scheduler.Schedule(schedule)
	if err != nil {
		t.Fatalf("schedule %q: %v", schedule.ChatUUID, err)
	}
	if _, err := r.guard.Register(schedule); err != nil {
		t.Fatalf("guard register %q: %v", schedule.ChatUUID, err)
	}
	r.mu.Lock()
	r.ids[schedule.Key()] = scheduleID
	r.chats[schedule.Key()] = schedule.ChatUUID
	r.schedules[schedule.Key()] = schedule
	r.mu.Unlock()
}

// TestSelfMessageTickDefersToTheLiveOwnerOfItsTicketLane is the ci#845
// regression for the self-message dispatcher path: a scheduled turn of a
// ticket+lane that already has a live owner (here the workflow dispatcher) must
// not fire, while an un-keyed schedule keeps its legacy behaviour.
func TestSelfMessageTickDefersToTheLiveOwnerOfItsTicketLane(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	runtime, scheduler := selfMessageCoordinationRuntime(t, &now)

	keyed := SelfMessageSchedule{
		BotUserID: 1, ChatUUID: "chat-keyed", Cron: "*/5 * * * *",
		Prompt: "resume", Ticket: "ci#845-selftest", Lane: "lane-a",
	}
	unkeyed := SelfMessageSchedule{
		BotUserID: 2, ChatUUID: "chat-unkeyed", Cron: "*/5 * * * *", Prompt: "resume",
	}
	runtime.selfMessageCoordinationInject(t, scheduler, keyed)
	runtime.selfMessageCoordinationInject(t, scheduler, unkeyed)

	fires := 0
	scheduler.SetEnqueue(func(SelfMessageSchedule, time.Time, string) error {
		fires++
		return nil
	})

	// The workflow dispatcher wins the ticket+lane first.
	guard := DefaultCoordinationOwnerGuard()
	if decision := guard.Decide(CoordinationDispatchRequest{
		Ticket: keyed.Ticket, Lane: keyed.Lane,
		Source:          CoordinationSourceWorkflowDispatch,
		InteractionUUID: "workflow-interaction-1",
	}); !decision.StartsNewInteraction() {
		t.Fatalf("workflow dispatch was not accepted: %+v", decision)
	}
	defer guard.Release(keyed.Ticket, keyed.Lane)

	now = now.Add(10 * time.Minute)
	if err := runtime.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if fires != 1 {
		t.Fatalf("fires = %d, want 1 (the owner-keyed schedule must defer, the un-keyed one must fire)", fires)
	}
	if live, ok := guard.Live(keyed.Ticket, keyed.Lane); !ok || live.InteractionUUID != "workflow-interaction-1" {
		t.Fatalf("the self-message replaced the workflow owner: %+v (ok=%v)", live, ok)
	}

	// Once the owner is gone the lane is free again and the schedule fires.
	guard.Release(keyed.Ticket, keyed.Lane)
	now = now.Add(10 * time.Minute)
	if err := runtime.Tick(); err != nil {
		t.Fatalf("tick after release: %v", err)
	}
	if fires != 3 {
		t.Fatalf("fires = %d, want 3 (two un-keyed runs plus the released owner-keyed run)", fires)
	}
	if _, ok := guard.Live(keyed.Ticket, keyed.Lane); !ok {
		t.Fatal("the fired self-message did not take ownership of the lane")
	}
}

// TestSelfMessageForgetReleasesTheOwnerKey checks the scheduler's drop path: a
// forgotten schedule must give its ticket+lane back to the dispatcher.
func TestSelfMessageForgetReleasesTheOwnerKey(t *testing.T) {
	now := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	runtime, scheduler := selfMessageCoordinationRuntime(t, &now)
	schedule := SelfMessageSchedule{
		BotUserID: 3, ChatUUID: "chat-forget", Cron: "*/5 * * * *",
		Prompt: "resume", Ticket: "ci#845-selftest-forget", Lane: "lane-b",
	}
	runtime.selfMessageCoordinationInject(t, scheduler, schedule)

	guard := DefaultCoordinationOwnerGuard()
	if decision := guard.Decide(CoordinationDispatchRequest{
		Ticket: schedule.Ticket, Lane: schedule.Lane, Source: CoordinationSourceSelfMessageSchedule,
	}); !decision.StartsNewInteraction() {
		t.Fatalf("the schedule did not take its lane: %+v", decision)
	}
	runtime.forget(schedule.Key())
	if _, ok := guard.Live(schedule.Ticket, schedule.Lane); ok {
		t.Fatal("forget kept the ticket+lane owned")
	}
}
