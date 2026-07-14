package msgmate

import (
	"testing"
	"time"
)

// newCoordinationTestRuntime builds the smallest harness that can drive one due
// self-message slot: a scheduler whose enqueue seam is a counter, a guard that
// knows the schedule, and the runtime's own bookkeeping. publishSeams is
// deliberately NOT called - it would install the DB-backed enqueue and the
// inspector idle check, neither of which this gate test needs.
func newCoordinationTestRuntime(t *testing.T, schedule SelfMessageSchedule, current *time.Time, enqueues *int) *SelfMessageRuntime {
	t.Helper()
	scheduler := NewSelfMessageScheduler()
	scheduler.SetNow(func() time.Time { return *current })
	scheduler.SetEnqueue(func(SelfMessageSchedule, time.Time, string) error {
		*enqueues++
		return nil
	})
	runtime := newSelfMessageRuntime(nil, nil, nil, scheduler, nil, time.Minute, func() time.Time { return *current })
	scheduleID, err := scheduler.Schedule(schedule)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, err := runtime.guard.Register(schedule); err != nil {
		t.Fatalf("guard register: %v", err)
	}
	key := schedule.Key()
	runtime.mu.Lock()
	runtime.ids[key] = scheduleID
	runtime.chats[key] = schedule.ChatUUID
	runtime.mu.Unlock()
	return runtime
}

// TestSelfMessageCoordinationGateDefersWhileAnotherLaneOwnerIsLive is the ci#845
// regression: a schedule whose chat declares a coordination lane must not fire
// while another interaction of that lane is live, and must fire once that owner
// is gone. Before ci#845 the second Tick delivered the self-message anyway.
func TestSelfMessageCoordinationGateDefersWhileAnotherLaneOwnerIsLive(t *testing.T) {
	current := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	enqueues := 0
	schedule := SelfMessageSchedule{
		BotUserID: 845,
		ChatUUID:  "chat-lane",
		Prompt:    "resume",
		Cron:      "*/20 * * * *",
		OwnerKey:  "owner:ci#845:default",
	}
	runtime := newCoordinationTestRuntime(t, schedule, &current, &enqueues)
	key := schedule.Key()
	if enqueues != 1 {
		t.Fatalf("registration enqueues = %d, want 1 (the first cron delivery)", enqueues)
	}

	runtime.SetCoordinationOwnerCheck(func(SelfMessageSchedule) (string, bool) {
		return "chat-other-owner", true
	})
	// The slot is due at 10:20; advance past it.
	current = time.Date(2024, 5, 1, 10, 21, 0, 0, time.UTC)
	if err := runtime.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if enqueues != 1 {
		t.Fatalf("the coordination gate must defer the fire, enqueues = %d, want 1", enqueues)
	}
	state, ok := runtime.guard.State(key)
	if !ok {
		t.Fatalf("the guard lost the schedule %s", key)
	}
	if state.Skips != 1 {
		t.Fatalf("skips = %d, want 1 (a recorded deferral)", state.Skips)
	}
	if state.LastSkipReason != selfMessageSkipOtherOwner {
		t.Fatalf("last skip reason = %q, want %q", state.LastSkipReason, selfMessageSkipOtherOwner)
	}
	if state.RunsUsed != 0 {
		t.Fatalf("runs used = %d, want 0 (nothing was delivered)", state.RunsUsed)
	}

	// Once the lane owner is no longer live the same due slot fires.
	runtime.SetCoordinationOwnerCheck(func(SelfMessageSchedule) (string, bool) {
		return "", false
	})
	if err := runtime.Tick(); err != nil {
		t.Fatalf("tick after the owner went idle: %v", err)
	}
	if enqueues != 2 {
		t.Fatalf("enqueues = %d, want 2 (the due slot fires once the lane is free)", enqueues)
	}
	if state, _ = runtime.guard.State(key); state.RunsUsed != 1 {
		t.Fatalf("runs used = %d, want 1 after the delivered fire", state.RunsUsed)
	}
	if state.LastSkipReason != "" {
		t.Fatalf("last skip reason = %q, want it cleared by the delivered run", state.LastSkipReason)
	}
}

// TestSelfMessageCoordinationGateIgnoresUnlanedSchedule pins the opt-in shape of
// the gate: a chat that never declared a lane has no owner key and is never
// deferred by the coordination seam, however loudly the seam reports an owner.
func TestSelfMessageCoordinationGateIgnoresUnlanedSchedule(t *testing.T) {
	current := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	enqueues := 0
	schedule := SelfMessageSchedule{
		BotUserID: 846,
		ChatUUID:  "chat-unlaned",
		Prompt:    "resume",
		Cron:      "*/20 * * * *",
	}
	runtime := newCoordinationTestRuntime(t, schedule, &current, &enqueues)
	key := schedule.Key()

	calls := 0
	runtime.SetCoordinationOwnerCheck(func(SelfMessageSchedule) (string, bool) {
		calls++
		return "chat-other-owner", true
	})
	current = time.Date(2024, 5, 1, 10, 21, 0, 0, time.UTC)
	if err := runtime.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if enqueues != 2 {
		t.Fatalf("enqueues = %d, want 2: an un-laned schedule must fire despite a live owner", enqueues)
	}
	if calls != 0 {
		t.Fatalf("the coordination seam was consulted %d times for an un-laned schedule, want 0", calls)
	}
	state, ok := runtime.guard.State(key)
	if !ok {
		t.Fatalf("the guard lost the schedule %s", key)
	}
	if state.Skips != 0 {
		t.Fatalf("skips = %d, want 0 for an un-laned schedule", state.Skips)
	}
}

// TestSelfMessageCoordinationOwnerLiveWithoutSeam reports no owner when no
// coordination seam was published, so the gate is off unless a deployment (or a
// test) opts in.
func TestSelfMessageCoordinationOwnerLiveWithoutSeam(t *testing.T) {
	current := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	enqueues := 0
	runtime := newCoordinationTestRuntime(t, SelfMessageSchedule{
		BotUserID: 847,
		ChatUUID:  "chat-lane",
		Prompt:    "resume",
		Cron:      "*/20 * * * *",
		OwnerKey:  "owner:ci#845:default",
	}, &current, &enqueues)

	if owner, live := runtime.coordinationOwnerLive(SelfMessageSchedule{OwnerKey: "owner:ci#845:default"}); live || owner != "" {
		t.Fatalf("a runtime without a coordination seam reported owner %q (live=%v)", owner, live)
	}
	var nilRuntime *SelfMessageRuntime
	if owner, live := nilRuntime.coordinationOwnerLive(SelfMessageSchedule{OwnerKey: "owner:ci#845:default"}); live || owner != "" {
		t.Fatalf("a nil runtime reported owner %q (live=%v)", owner, live)
	}
}
