package workqueue

import (
	"testing"

	"github.com/hibiken/asynq"
)

// TestClassifyBotReplyLivenessCountsOrphanedActiveTasks is the ci#1195 half the
// ci#609 stall guard could not see: asynq keeps listing a task as *active* after
// the worker that leased it disappeared. Treating that as a live turn is what
// kept a native interaction `state=active` with no first turn and no error
// surface, so the classification must report the dead work as not live.
func TestClassifyBotReplyLivenessCountsOrphanedActiveTasks(t *testing.T) {
	tasks := []*asynq.TaskInfo{
		{ID: "bot-reply:chat-1", State: asynq.TaskStateActive, IsOrphaned: true},
		{ID: "bot-reply:chat-2", State: asynq.TaskStateActive},
		{ID: "bot-reply:chat-3", State: asynq.TaskStatePending},
		nil,
	}

	liveness := classifyBotReplyLiveness(tasks)
	if liveness.Active != 2 || liveness.Orphaned != 1 {
		t.Fatalf("expected 2 active / 1 orphaned, got %d/%d", liveness.Active, liveness.Orphaned)
	}
	if !liveness.TurnLive() {
		t.Fatalf("one healthy active task must keep the turn live")
	}
	if liveness.Frozen() {
		t.Fatalf("a listing with a healthy active task is not frozen")
	}
}

// TestBotReplyLivenessFrozenOnlyWhenEveryActiveTaskIsOrphaned pins the freeze
// decision: only dead work that asynq still calls active is a freeze, an empty
// listing is a plain idle chat, not a stall.
func TestBotReplyLivenessFrozenOnlyWhenEveryActiveTaskIsOrphaned(t *testing.T) {
	cases := []struct {
		name   string
		tasks  []*asynq.TaskInfo
		live   bool
		frozen bool
	}{
		{name: "no tasks", tasks: nil},
		{
			name:   "orphaned only",
			tasks:  []*asynq.TaskInfo{{ID: "bot-reply:chat-1", State: asynq.TaskStateActive, IsOrphaned: true}},
			frozen: true,
		},
		{
			name:  "live worker",
			tasks: []*asynq.TaskInfo{{ID: "bot-reply:chat-1", State: asynq.TaskStateActive}},
			live:  true,
		},
		{
			name: "orphaned plus live continuation",
			tasks: []*asynq.TaskInfo{
				{ID: "bot-reply:chat-1", State: asynq.TaskStateActive, IsOrphaned: true},
				{ID: "bot-reply:chat-1:msg-2", State: asynq.TaskStateActive},
			},
			live: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			liveness := classifyBotReplyLiveness(tc.tasks)
			if liveness.TurnLive() != tc.live {
				t.Fatalf("TurnLive()=%v, want %v", liveness.TurnLive(), tc.live)
			}
			if liveness.Frozen() != tc.frozen {
				t.Fatalf("Frozen()=%v, want %v", liveness.Frozen(), tc.frozen)
			}
		})
	}
}

// TestBotReplyOrphanHelpersAreNilSafe keeps the liveness helpers usable from
// every caller that may run without a queue (unit tests, degraded deployments).
func TestBotReplyOrphanHelpersAreNilSafe(t *testing.T) {
	if BotReplyActiveOrphaned(nil, "chat-1") {
		t.Fatalf("no inspector must never report frozen work")
	}
	if BotReplyWaitingWork(nil, "chat-1") {
		t.Fatalf("no inspector must never report waiting work")
	}
	if BotReplyActiveOrphaned(nil, "") || BotReplyWaitingWork(nil, "") {
		t.Fatalf("empty chat UUID must never report work")
	}
}
