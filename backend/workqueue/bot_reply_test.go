package workqueue

import (
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

// TestBotReplyTaskIDStable pins the per-chat task ID used for asynq's
// duplicate-enqueue guard and lease. It must be a pure function of the chat so
// every process derives the same key (ci#648).
func TestBotReplyTaskIDStable(t *testing.T) {
	const chat = "chat-123"
	first := BotReplyTaskID(chat)
	second := BotReplyTaskID(chat)
	if first != "bot-reply:"+chat {
		t.Fatalf("unexpected task id: %q", first)
	}
	if first != second {
		t.Fatalf("BotReplyTaskID must be stable for the same chat")
	}
	if first == BotReplyTaskID("chat-124") {
		t.Fatalf("BotReplyTaskID must differ across chats")
	}
}

// TestEnqueueBotReplyIdempotency exercises the per-chat dedupe on a real asynq
// server backed by miniredis: a second enqueue for the same chat while the
// first is still pending is rejected with ErrTaskIdConflict, and enqueueing
// succeeds again once the queued task is cancelled (ci#648).
func TestEnqueueBotReplyIdempotency(t *testing.T) {
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

	payload := BotReplyPayload{ChatUUID: "chat-1", MessageUUID: "msg-1", BotUserID: 1}

	// A nil inspector disables the pre-enqueue cancel, so the raw duplicate
	// guard is what we observe.
	if _, err := EnqueueBotReply(client, nil, payload); err != nil {
		if isUnsupportedScriptError(err) {
			t.Skipf("miniredis cannot run asynq scripts: %v", err)
		}
		t.Fatalf("first enqueue failed: %v", err)
	}

	if _, err := EnqueueBotReply(client, nil, payload); !errors.Is(err, asynq.ErrTaskIDConflict) {
		t.Fatalf("second enqueue for the same chat: expected ErrTaskIDConflict, got %v", err)
	}

	CancelBotReplyTask(inspector, payload.ChatUUID)

	if _, err := EnqueueBotReply(client, nil, payload); err != nil {
		t.Fatalf("enqueue after CancelBotReplyTask failed: %v", err)
	}
}

// TestBotReplyQueuedOrActive covers the queued-but-not-yet-running window the
// agent-dispatch dedupe guard relies on (ci#625): a reply enqueued but not yet
// picked up by a worker counts as live, while a cancelled/absent one does not,
// and a nil inspector never reports liveness.
func TestBotReplyQueuedOrActive(t *testing.T) {
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

	const chat = "chat-queued"
	payload := BotReplyPayload{ChatUUID: chat, MessageUUID: "msg-1", BotUserID: 1}

	if BotReplyQueuedOrActive(inspector, chat) {
		t.Fatalf("no task enqueued yet: BotReplyQueuedOrActive = true, want false")
	}
	if BotReplyQueuedOrActive(inspector, "") {
		t.Fatalf("empty chat uuid: BotReplyQueuedOrActive = true, want false")
	}
	if BotReplyQueuedOrActive(nil, chat) {
		t.Fatalf("nil inspector: BotReplyQueuedOrActive = true, want false")
	}

	if _, err := EnqueueBotReply(client, nil, payload); err != nil {
		if isUnsupportedScriptError(err) {
			t.Skipf("miniredis cannot run asynq scripts: %v", err)
		}
		t.Fatalf("enqueue failed: %v", err)
	}

	// Enqueued but not consumed: still live for dedupe purposes.
	if !BotReplyQueuedOrActive(inspector, chat) {
		t.Fatalf("queued task: BotReplyQueuedOrActive = false, want true")
	}
	if BotReplyQueuedOrActive(inspector, "chat-other") {
		t.Fatalf("unrelated chat: BotReplyQueuedOrActive = true, want false")
	}

	CancelBotReplyTask(inspector, chat)
	if BotReplyQueuedOrActive(inspector, chat) {
		t.Fatalf("after cancel: BotReplyQueuedOrActive = true, want false")
	}
}

func isUnsupportedScriptError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "script") ||
		strings.Contains(msg, "eval") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "not supported")
}
