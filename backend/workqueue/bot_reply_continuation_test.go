package workqueue

import (
	"testing"

	"github.com/hibiken/asynq"
)

// TestDecideBotReplyContinuationNeverQueuesBehindLiveTurn pins the ci#1142
// invariant: the follow-up turn of a confirmable action must never be attached
// behind a turn that is still reported as live. That turn is exactly the one
// that did not take the decision (registry gone, another instance, expired
// park), so queueing behind it starves the continuation for the park TTL while
// every liveness check keeps reporting the chat as busy.
func TestDecideBotReplyContinuationNeverQueuesBehindLiveTurn(t *testing.T) {
	for _, turnLive := range []bool{false, true} {
		if got := DecideBotReplyContinuation(turnLive); got != BotReplyDecisionInterruptTurn {
			t.Fatalf("DecideBotReplyContinuation(turnLive=%v) = %q, want %q", turnLive, got, BotReplyDecisionInterruptTurn)
		}
	}
}

// TestEnqueueBotReplyContinuationTakesOverOrphanedLiveTurn is the ci#1142
// regression: an approved confirmable action with continue_after_execute=true
// found no live park, but the chat still had a *live* reply task (the parked
// turn whose decision registry is gone), so the old fallback called
// EnqueueBotReply and queued the continuation behind it under a per-message task
// ID. The parked turn then either ran out its 10-minute park TTL or never
// finished at all, no bot turn was ever produced, and the interaction was
// reported as active for the whole 720s window and beyond.
//
// The continuation must instead replace the orphaned live turn: the follow-up
// turn has to exist as the chat's base bot-reply task immediately.
func TestEnqueueBotReplyContinuationTakesOverOrphanedLiveTurn(t *testing.T) {
	client, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	// The orphaned turn: a live bot-reply task for the chat under the base ID,
	// exactly like the parked generation that never consumed the decision.
	orphaned, err := NewBotReplyTask(payload)
	if err != nil {
		t.Fatalf("building the orphaned task failed: %v", err)
	}
	stale, err := client.Enqueue(orphaned, asynq.TaskID(BotReplyTaskID(payload.ChatUUID)))
	if err != nil {
		if isUnsupportedScriptError(err) {
			t.Skipf("miniredis cannot run asynq scripts: %v", err)
		}
		t.Fatalf("seeding the orphaned live turn failed: %v", err)
	}

	continuation := payload
	continuation.MessageUUID = "confirmable-executed-event"
	info, err := enqueueBotReplyContinuation(client, inspector, continuation, true)
	if err != nil {
		t.Fatalf("the continuation was not enqueued: %v", err)
	}
	if info == nil {
		t.Fatal("continuation enqueue returned no task info")
	}

	// The old behaviour produced bot-reply:<chat>:<message> behind the orphan.
	if got, want := info.ID, BotReplyTaskID(payload.ChatUUID); got != want {
		t.Fatalf("continuation task id = %q, want the base id %q (queueing behind the orphaned live turn starves the follow-up turn, ci#1142)", got, want)
	}
	if info.ID == stale.ID && info.Type != stale.Type {
		t.Fatalf("continuation did not produce a fresh bot reply task (%q)", info.ID)
	}

	// And the follow-up turn must actually be resolvable in the queue: if the
	// enqueue had failed, the chat would stay active with no bot turn.
	if _, err := inspector.GetTaskInfo(QueueDefault, info.ID); err != nil {
		t.Fatalf("the continuation task %q is not in the queue: %v", info.ID, err)
	}
}
