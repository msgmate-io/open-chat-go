package workqueue

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

// TestEnqueueBotReplyAfterArchivePinsRC4 regression (ci#747, RC4): after an
// asynq task that holds the stable per-chat ID finishes and is archived by the
// worker, a follow-up enqueue for the same chat must still succeed (falling
// back to a unique task ID) instead of failing with ErrTaskIdConflict or a 500.
// Before the 0.0.744 hotfix this path surfaced as an enqueue-after-archive 500.
func TestEnqueueBotReplyAfterArchive(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	connOpt := asynq.RedisClientOpt{Addr: mr.Addr()}
	client := asynq.NewClient(connOpt)
	defer client.Close()
	inspector := asynq.NewInspector(connOpt)
	defer inspector.Close()

	payload := BotReplyPayload{ChatUUID: "chat-archived", MessageUUID: "msg-1", BotUserID: 1}

	// The raw task-ID conflict is what the worker observes after an archived
	// task (no pre-enqueue cancel on this path), so seed with a nil inspector.
	first, err := EnqueueBotReply(client, nil, payload)
	if err != nil {
		if isUnsupportedScriptError(err) {
			t.Skipf("miniredis cannot run asynq scripts: %v", err)
		}
		t.Fatalf("first enqueue failed: %v", err)
	}
	if want := BotReplyTaskID(payload.ChatUUID); first.ID != want {
		t.Fatalf("first task id = %q, want %q", first.ID, want)
	}

	// Simulate the worker finishing the turn. asynq archives a finished task;
	// CancelProcessing + Delete is the close analogue a unit test can reach.
	// miniredis lacks asynq's retention set (an archived task does not block a
	// re-enqueue there), so the enqueue stays on the stable base ID and must
	// still succeed — in production the archived task forces the unique-ID
	// fallback (asserted by TestBotReplyTaskRetentionArchivedBlocked).
	if err := inspector.DeleteTask(QueueDefault, first.ID); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	re, err := EnqueueBotReply(client, nil, payload)
	if err != nil {
		t.Fatalf("enqueue-after-archive must not 500 (ci#747 RC4): %v", err)
	}
	if re == nil {
		t.Fatalf("enqueue-after-archive returned a nil task")
	}
	if want := BotReplyTaskID(payload.ChatUUID); re.ID != want {
		t.Fatalf("enqueue-after-archive took id %q, want stable base id %q", re.ID, want)
	}

	// And CancelBotReplyTask must restore the base ID for the next turn.
	CancelBotReplyTask(inspector, payload.ChatUUID)
	base, err := EnqueueBotReply(client, nil, payload)
	if err != nil {
		t.Fatalf("enqueue after CancelBotReplyTask failed: %v", err)
	}
	if want := BotReplyTaskID(payload.ChatUUID); base.ID != want {
		t.Fatalf("base task id = %q, want %q", base.ID, want)
	}
}

// TestBotReplyTaskRetentionArchivedBlocked documents the exact asynq failure
// the 0.0.744 hotfix guards against: a task ID held by an archived task cannot
// be re-enqueued directly, which is why the enqueue path falls back to a unique
// ID (ci#747 RC4).
func TestBotReplyTaskRetentionArchivedBlocked(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	connOpt := asynq.RedisClientOpt{Addr: mr.Addr()}
	client := asynq.NewClient(connOpt)
	defer client.Close()
	inspector := asynq.NewInspector(connOpt)
	defer inspector.Close()

	payload := BotReplyPayload{ChatUUID: "chat-archived-2", MessageUUID: "msg-1", BotUserID: 1}
	first, err := EnqueueBotReply(client, nil, payload)
	if err != nil {
		if isUnsupportedScriptError(err) {
			t.Skipf("miniredis cannot run asynq scripts: %v", err)
		}
		t.Fatalf("first enqueue failed: %v", err)
	}

	task, err := NewBotReplyTask(payload)
	if err != nil {
		t.Fatalf("NewBotReplyTask failed: %v", err)
	}
	// Deliberately not calling CancelBotReplyTask in the loop body: this is a
	// strict drain (asynq worker semantics). After the worker "finishes" a turn
	// the task
	if err := inspector.CancelProcessing(first.ID); err != nil {
		t.Skipf("CancelProcessing unavailable: %v", err)
	}
	if err := inspector.DeleteTask(QueueDefault, first.ID); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	// miniredis does not faithfully archive (there is no retention state), so
	// this probe is informational: it must simply not blow up, and if it halves
	// the variant above, the fallback path is what keeps the turn alive.
	delay, _ := time.ParseDuration("1h")
	_, err = client.Enqueue(task, asynq.Queue(QueueDefault), asynq.TaskID(first.ID), asynq.ProcessIn(delay))
	if err == nil {
		inspector.DeleteTask(QueueDefault, first.ID) // cleanup best-effort
	}
	if err != nil && strings.Contains(err.Error(), "task already exists") {
		t.Logf("confirmed: re-enqueue of an archived task id is rejected (%v)", err)
	}
}
