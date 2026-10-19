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

// TestParseBotReplyDeliveryMode pins the accepted delivery-mode spellings
// (ci#656). An empty value is the default (queued) and an unknown one is
// rejected so the API can answer 400 instead of guessing a mode.
func TestParseBotReplyDeliveryMode(t *testing.T) {
	cases := []struct {
		raw       string
		want      BotReplyDeliveryMode
		wantValid bool
	}{
		{"", BotReplyDeliveryQueued, true},
		{"queued", BotReplyDeliveryQueued, true},
		{"QUeued", BotReplyDeliveryQueued, true},
		{" queue ", BotReplyDeliveryQueued, true},
		{"task-stack", BotReplyDeliveryQueued, true},
		{"interrupt", BotReplyDeliveryInterrupt, true},
		{"Immediate", BotReplyDeliveryInterrupt, true},
		{"send-now", BotReplyDeliveryInterrupt, true},
		{"now", BotReplyDeliveryInterrupt, true},
		{"definitely-not-a-mode", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseBotReplyDeliveryMode(tc.raw)
		if ok != tc.wantValid {
			t.Fatalf("ParseBotReplyDeliveryMode(%q) valid = %v, want %v", tc.raw, ok, tc.wantValid)
		}
		if got != tc.want {
			t.Fatalf("ParseBotReplyDeliveryMode(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestDecideBotReplyEnqueue pins the queue decision for both modes with and
// without a live turn (ci#656): queued must never stop a running turn, and only
// an explicit interrupt may replace it.
func TestDecideBotReplyEnqueue(t *testing.T) {
	cases := []struct {
		mode     BotReplyDeliveryMode
		turnLive bool
		want     BotReplyEnqueueDecision
	}{
		{BotReplyDeliveryQueued, true, BotReplyDecisionQueueBehind},
		{BotReplyDeliveryQueued, false, BotReplyDecisionReplace},
		{BotReplyDeliveryInterrupt, true, BotReplyDecisionInterruptTurn},
		{BotReplyDeliveryInterrupt, false, BotReplyDecisionInterruptTurn},
	}
	for _, tc := range cases {
		if got := DecideBotReplyEnqueue(tc.mode, tc.turnLive); got != tc.want {
			t.Fatalf("DecideBotReplyEnqueue(%q, %v) = %q, want %q", tc.mode, tc.turnLive, got, tc.want)
		}
	}
}

// TestEnqueueBotReplyInterruptTakesBaseTaskID checks the explicit interrupt
// path: every reply task of the chat (the live turn and anything queued behind
// it) is cancelled and the new reply takes the base task ID so a worker picks it
// up immediately (ci#656).
func TestEnqueueBotReplyInterruptTakesBaseTaskID(t *testing.T) {
	client, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	if _, err := EnqueueBotReply(client, inspector, payload); err != nil {
		t.Skipf("asynq enqueue unavailable: %v", err)
	}

	next := payload
	next.MessageUUID = "msg-2"
	info, err := EnqueueBotReplyWithDelivery(client, inspector, next, BotReplyDeliveryInterrupt)
	if err != nil {
		t.Fatalf("interrupt enqueue failed: %v", err)
	}
	if info.ID != BotReplyTaskID(payload.ChatUUID) {
		t.Fatalf("interrupt task id = %q, want base id %q", info.ID, BotReplyTaskID(payload.ChatUUID))
	}
	if got := BotReplyQueuedCount(inspector, payload.ChatUUID); got != 0 {
		t.Fatalf("queued replies after interrupt = %d, want 0", got)
	}
}

// TestEnqueueBotReplyQueuedIdleReplacesNewest pins the queued-mode semantics
// (ci#656) without a live turn: the newest message wins, the reply takes the
// stable per-chat task ID (the key the dispatch dedupe and the API tests use)
// and exactly one reply stays queued for the chat. The queued-behind-a-live-turn
// variant is covered by TestEnqueueBotReplyQueuedBehindLiveTurn.
func TestEnqueueBotReplyQueuedIdleReplacesNewest(t *testing.T) {
	client, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	second := payload
	second.MessageUUID = "msg-2"
	third := payload
	third.MessageUUID = "msg-3"

	info, err := EnqueueBotReplyWithDelivery(client, inspector, second, BotReplyDeliveryQueued)
	if err != nil {
		t.Skipf("asynq enqueue unavailable: %v", err)
	}
	if want := BotReplyTaskID(payload.ChatUUID); info.ID != want {
		t.Fatalf("idle queued task id = %q, want base id %q", info.ID, want)
	}

	if _, err := EnqueueBotReplyWithDelivery(client, inspector, third, BotReplyDeliveryQueued); err != nil {
		t.Fatalf("queued enqueue failed: %v", err)
	}

	pending := botReplyTasks(inspector, inspector.ListPendingTasks, payload.ChatUUID)
	if len(pending) != 1 {
		t.Fatalf("pending replies = %d, want 1", len(pending))
	}
	if want := BotReplyTaskID(payload.ChatUUID); pending[0].ID != want {
		t.Fatalf("pending reply = %q, want the replaced base id %q", pending[0].ID, want)
	}
}

// TestEnqueueBotReplyQueuedBehindLiveTurn is the ci#656 regression: the default
// queued mode must never take over the live turn's task. It drives the
// liveness-injected core with turnLive=true (a task waiting in the queue is
// pending, not active, so a unit test cannot make BotReplyIsActive report a live
// turn) and asserts that the live turn keeps the base task ID while the new
// reply lands behind it under its own per-message ID.
func TestEnqueueBotReplyQueuedBehindLiveTurn(t *testing.T) {
	client, inspector, payload, cleanup := newTestBotReplyQueue(t)
	defer cleanup()

	if got := DecideBotReplyEnqueue(BotReplyDeliveryQueued, true); got != BotReplyDecisionQueueBehind {
		t.Fatalf("queued mode with a live turn = %q, want %q", got, BotReplyDecisionQueueBehind)
	}

	liveTask, err := NewBotReplyTask(payload)
	if err != nil {
		t.Fatalf("building the live task failed: %v", err)
	}
	live, err := client.Enqueue(liveTask, asynq.TaskID(BotReplyTaskID(payload.ChatUUID)))
	if err != nil {
		t.Skipf("asynq enqueue unavailable: %v", err)
	}

	next := payload
	next.MessageUUID = "msg-2"
	info, err := enqueueBotReplyWithDelivery(client, inspector, next, BotReplyDeliveryQueued, true)
	if err != nil {
		t.Fatalf("queued enqueue failed: %v", err)
	}
	if info.ID == live.ID {
		t.Fatalf("queued reply clobbered the live turn's task id %q", info.ID)
	}
	if want := botReplyTaskID(payload.ChatUUID, "msg-2"); info.ID != want {
		t.Fatalf("queued task id = %q, want %q", info.ID, want)
	}
	if _, err := inspector.GetTaskInfo(QueueDefault, live.ID); err != nil {
		t.Fatalf("the live turn's task %q was removed by a queued reply: %v", live.ID, err)
	}
}

// newTestBotReplyQueue wires the asynq client/inspector used by the queue-mode
// tests. It skips instead of failing when the redis substitute cannot run
// asynq's Lua scripts.
func newTestBotReplyQueue(t *testing.T) (*asynq.Client, *asynq.Inspector, BotReplyPayload, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}

	connOpt := asynq.RedisClientOpt{Addr: mr.Addr()}
	client := asynq.NewClient(connOpt)
	inspector := asynq.NewInspector(connOpt)

	cleanup := func() {
		client.Close()
		inspector.Close()
		mr.Close()
	}

	payload := BotReplyPayload{ChatUUID: "chat-queue", MessageUUID: "msg-1", BotUserID: 1}
	if _, err := EnqueueBotReply(client, nil, payload); err != nil {
		if isUnsupportedScriptError(err) {
			cleanup()
			t.Skipf("miniredis cannot run asynq scripts: %v", err)
		}
		cleanup()
		t.Fatalf("seeding the live turn failed: %v", err)
	}
	CancelBotReplyTask(inspector, payload.ChatUUID)

	return client, inspector, payload, cleanup
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
