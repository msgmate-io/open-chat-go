package workqueue

import (
	"testing"

	"github.com/hibiken/asynq"
)

// TestVerifyBotReplyContinuationQueuedToleratesMissingInspector pins the
// ci#1148 read-back contract for callers that have no inspector: they keep the
// best-effort enqueue instead of failing.
func TestVerifyBotReplyContinuationQueuedToleratesMissingInspector(t *testing.T) {
	payload := BotReplyPayload{ChatUUID: "chat-verify", MessageUUID: "msg-1"}
	if err := verifyBotReplyContinuationQueued(nil, payload, &asynq.TaskInfo{ID: "bot-reply:chat-verify"}); err != nil {
		t.Fatalf("a nil inspector must not fail the continuation: %v", err)
	}
	if err := verifyBotReplyContinuationQueued(nil, payload, nil); err != nil {
		t.Fatalf("a nil task info must not fail the continuation: %v", err)
	}
}

// TestVerifyBotReplyContinuationQueuedReportsLostTask is the ci#1148
// regression: the confirmable action reported continuation=continuation_queued
// while the interaction stayed "active" with no bot message and hit the 720s
// timeout. A continuation that asynq accepted but that is not readable in the
// queue must surface as an error at the decision point, not stay silent.
func TestVerifyBotReplyContinuationQueuedReportsLostTask(t *testing.T) {
	_, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	err := verifyBotReplyContinuationQueued(inspector, payload, &asynq.TaskInfo{
		ID:    "bot-reply:chat-that-was-never-enqueued",
		Queue: QueueDefault,
		Type:  TypeBotReply,
	})
	if err == nil {
		t.Fatal("a continuation task that cannot be read back from the queue must be reported as an error (ci#1148)")
	}
}

// TestVerifyBotReplyContinuationQueuedAcceptsQueuedTask checks the happy path:
// a task that is actually sitting in the queue is accepted.
func TestVerifyBotReplyContinuationQueuedAcceptsQueuedTask(t *testing.T) {
	client, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	task, err := NewBotReplyTask(payload)
	if err != nil {
		t.Fatalf("building the continuation task failed: %v", err)
	}
	info, err := client.Enqueue(task)
	if err != nil {
		if isUnsupportedScriptError(err) {
			t.Skipf("miniredis cannot run asynq scripts: %v", err)
		}
		t.Fatalf("enqueueing the continuation failed: %v", err)
	}
	if err := verifyBotReplyContinuationQueued(inspector, payload, info); err != nil {
		t.Fatalf("the queued continuation was rejected by the read-back: %v", err)
	}
}
