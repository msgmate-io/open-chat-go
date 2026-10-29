package workqueue

import (
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

// TestEnqueueBotReplyAfterArchive is the ci#918 regression: a follow-up enqueue
// for the same chat after the worker finished and archived the bot-reply task
// must not fail with ErrTaskIdConflict (500). asynq retains the archived task
// under its stable per-chat ID, so the second enqueue would be rejected; the
// hotfix (shipped as 0.0.744 via the ci#768 canary) absorbs that conflict by
// falling back to a unique task ID. This pins that contract end to end: the
// archived task stays in the archive (retention), yet the new reply is queued
// and reachable.
func TestEnqueueBotReplyAfterArchive(t *testing.T) {
	client, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	baseID := BotReplyTaskID(payload.ChatUUID)

	if _, err := EnqueueBotReply(client, inspector, payload); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}

	// The worker finishes (or fails) the reply and asynq archives it, keeping
	// the task key so the ID stays held. Inspector.ArchiveTask lands the same
	// end state the worker's archive path produces (task key present, state
	// archived, id in the archived zset) without needing a live worker.
	if err := inspector.ArchiveTask(QueueDefault, baseID); err != nil {
		t.Fatalf("archiving the bot reply failed: %v", err)
	}
	archivedInfo, err := inspector.GetTaskInfo(QueueDefault, baseID)
	if err != nil {
		t.Fatalf("getting the archived reply failed: %v", err)
	}
	if archivedInfo.State != asynq.TaskStateArchived {
		t.Fatalf("archived reply state = %v, want %v", archivedInfo.State, asynq.TaskStateArchived)
	}

	next := payload
	next.MessageUUID = "msg-2"

	// Follow-up enqueue for the same chat after the archive: must not surface
	// ErrTaskIdConflict (the 500). The base ID is still held by the archived
	// task, so the reply lands under the unique fallback ID.
	info, err := EnqueueBotReply(client, inspector, next)
	if err != nil {
		t.Fatalf("enqueue after archive must not fail: %v", err)
	}
	if info.ID == baseID {
		t.Fatalf("enqueue after archive reused the archived base id %q", baseID)
	}

	// The fallback reply is actually queued and reachable, and the archived
	// task from the finished turn is retained untouched.
	queuedInfo, err := inspector.GetTaskInfo(QueueDefault, info.ID)
	if err != nil {
		t.Fatalf("follow-up reply %q is not in the queue: %v", info.ID, err)
	}
	if queuedInfo.State != asynq.TaskStatePending {
		t.Fatalf("follow-up reply state = %v, want %v", queuedInfo.State, asynq.TaskStatePending)
	}
	archived := botReplyTasks(inspector, inspector.ListArchivedTasks, payload.ChatUUID)
	if len(archived) != 1 || archived[0].ID != baseID {
		t.Fatalf("archived replies = %+v, want exactly the retained base id %q", archived, baseID)
	}
}

// TestBotReplyTaskRetentionArchivedBlocked documents the exact asynq retention
// failure the 0.0.744 hotfix guards against: archiving a bot reply keeps the
// task under its stable per-chat ID (retention), so a naive re-enqueue with
// that same ID is rejected with ErrTaskIdConflict. The API surfaced that as a
// 500 before the hotfix; the enqueue path must therefore never rely on the
// archived ID being free.
func TestBotReplyTaskRetentionArchivedBlocked(t *testing.T) {
	client, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	baseID := BotReplyTaskID(payload.ChatUUID)

	if _, err := EnqueueBotReply(client, inspector, payload); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}

	if err := inspector.ArchiveTask(QueueDefault, baseID); err != nil {
		t.Fatalf("archiving the bot reply failed: %v", err)
	}

	// The archived task is retained and visible in the archive.
	archived := botReplyTasks(inspector, inspector.ListArchivedTasks, payload.ChatUUID)
	if len(archived) != 1 || archived[0].ID != baseID {
		t.Fatalf("archived replies = %+v, want exactly the retained base id %q", archived, baseID)
	}

	// Raw re-enqueue with the same task ID is exactly the blocked operation:
	// asynq still holds the key, so it answers ErrTaskIDConflict (which the
	// pre-hotfix code surfaced as a 500). This pins the failure mode itself,
	// not just the fallback around it.
	task, err := NewBotReplyTask(payload)
	if err != nil {
		t.Fatalf("building the reply task failed: %v", err)
	}
	if _, err := client.Enqueue(task, asynq.TaskID(baseID)); err == nil {
		t.Fatalf("re-enqueue with the retained archived id %q unexpectedly succeeded", baseID)
	} else if !errors.Is(err, asynq.ErrTaskIDConflict) {
		t.Fatalf("re-enqueue with the retained archived id %q = %v, want ErrTaskIDConflict", baseID, err)
	}
}
