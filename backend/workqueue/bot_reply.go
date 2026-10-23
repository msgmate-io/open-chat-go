package workqueue

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
)

// defaultBotReplyTimeout bounds a single bot reply turn. It is deliberately
// generous: a native coder turn makes many tool calls (each a provider
// round-trip and often a sandbox operation) and routinely runs for well over
// five minutes, so the previous 5-minute cap cut such turns off mid-work and
// mislabelled them as user cancellations ("I paused this response", ci#642).
// A genuine user stop still aborts the turn immediately through the chat
// interrupt endpoint, so the deadline is only a backstop against a hung turn.
const defaultBotReplyTimeout = 30 * time.Minute

// BotReplyTimeoutEnv overrides the bot reply turn deadline with a Go duration
// (e.g. "45m"); an empty, invalid or non-positive value falls back to the
// default.
const BotReplyTimeoutEnv = "OPEN_CHAT_BOT_REPLY_TIMEOUT"

// BotReplyTimeout resolves the deadline applied to a bot reply task.
func BotReplyTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(BotReplyTimeoutEnv))
	if raw == "" {
		return defaultBotReplyTimeout
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return defaultBotReplyTimeout
	}
	return parsed
}

// BotReplyTaskID returns a stable asynq task ID for the bot reply in a chat.
func BotReplyTaskID(chatUUID string) string {
	return "bot-reply:" + chatUUID
}

// botReplyTaskID returns the asynq task ID for a bot reply. The base ID is used
// for a chat's reply when no turn is running; while a turn is live, further
// messages queue behind it under a unique ID (ci#656).
func botReplyTaskID(chatUUID, messageUUID string) string {
	if strings.TrimSpace(messageUUID) == "" {
		return BotReplyTaskID(chatUUID)
	}
	return BotReplyTaskID(chatUUID) + ":" + messageUUID
}

// botReplyQueuePrefix matches every reply task for a chat: the base ID and the
// unique IDs of replies queued behind a live turn.
func botReplyQueuePrefix(chatUUID string) string {
	return BotReplyTaskID(chatUUID) + ":"
}

// botReplyTasks lists the reply tasks of a chat in the given state.
func botReplyTasks(inspector *asynq.Inspector, list func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error), chatUUID string) []*asynq.TaskInfo {
	if inspector == nil {
		return nil
	}
	base := BotReplyTaskID(chatUUID)
	prefix := botReplyQueuePrefix(chatUUID)
	tasks, err := list(QueueDefault, asynq.PageSize(100))
	if err != nil {
		return nil
	}
	matched := make([]*asynq.TaskInfo, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == base || strings.HasPrefix(task.ID, prefix) {
			matched = append(matched, task)
		}
	}
	return matched
}

// BotReplyIsActive reports whether a turn is live for the chat. A live turn is
// never cancelled by a new message or a continuation (ci#656): the new reply is
// queued behind it instead.
func BotReplyIsActive(inspector *asynq.Inspector, chatUUID string) bool {
	return len(botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID)) > 0
}

// BotReplyHasWork reports whether a reply is queued or running for the chat.
// Unlike BotReplyIsActive it also counts pending, scheduled and retry tasks, so
// liveness checks (dispatch dedupe, stall detection) do not mistake a reply
// waiting for a worker for an idle chat.
func BotReplyHasWork(inspector *asynq.Inspector, chatUUID string) bool {
	if inspector == nil || chatUUID == "" {
		return false
	}
	if len(botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID)) > 0 {
		return true
	}
	if len(botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID)) > 0 {
		return true
	}
	if len(botReplyTasks(inspector, inspector.ListScheduledTasks, chatUUID)) > 0 {
		return true
	}
	return len(botReplyTasks(inspector, inspector.ListRetryTasks, chatUUID)) > 0
}

// BotReplyDeliveryMode selects how a message sent into a chat that is already
// running a turn is delivered (ci#656).
type BotReplyDeliveryMode string

const (
	// BotReplyDeliveryQueued appends the reply behind the live turn: the work
	// already done is preserved and the message is answered once the turn
	// completes. It is the default for a message into a running chat.
	BotReplyDeliveryQueued BotReplyDeliveryMode = "queued"
	// BotReplyDeliveryInterrupt stops the live turn now and answers the message
	// immediately. The partial progress of the stopped turn (files, commits) is
	// already persisted, so nothing is lost.
	BotReplyDeliveryInterrupt BotReplyDeliveryMode = "interrupt"
)

// ParseBotReplyDeliveryMode normalises a client-supplied delivery mode. An
// empty value means the default (queued). It reports false for an unknown mode
// so callers reject it instead of silently guessing.
func ParseBotReplyDeliveryMode(raw string) (BotReplyDeliveryMode, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", string(BotReplyDeliveryQueued), "queue", "task-stack", "task_stack":
		return BotReplyDeliveryQueued, true
	case string(BotReplyDeliveryInterrupt), "immediate", "send_now", "send-now", "now":
		return BotReplyDeliveryInterrupt, true
	default:
		return "", false
	}
}

// BotReplyEnqueueDecision names how a new reply interacts with the reply tasks
// a chat already has. It is a pure function of the requested delivery mode and
// whether a turn is live, so the queue behaviour is testable without a queue.
type BotReplyEnqueueDecision string

const (
	// BotReplyDecisionReplace drops the queued-but-unstarted replies: nothing
	// is running, the newest message wins.
	BotReplyDecisionReplace BotReplyEnqueueDecision = "replace"
	// BotReplyDecisionQueueBehind keeps the live turn and appends the reply
	// behind it.
	BotReplyDecisionQueueBehind BotReplyEnqueueDecision = "queue-behind"
	// BotReplyDecisionInterruptTurn stops the live turn and starts the new one.
	BotReplyDecisionInterruptTurn BotReplyEnqueueDecision = "interrupt-turn"
)

// DecideBotReplyEnqueue maps a delivery mode and the live-turn state to the
// enqueue decision (ci#656).
func DecideBotReplyEnqueue(mode BotReplyDeliveryMode, turnLive bool) BotReplyEnqueueDecision {
	if mode == BotReplyDeliveryInterrupt {
		return BotReplyDecisionInterruptTurn
	}
	if turnLive {
		return BotReplyDecisionQueueBehind
	}
	return BotReplyDecisionReplace
}

// BotReplyQueuedCount reports how many replies are queued behind the chat's
// live turn, ie how many messages the transcript should render as pending
// (ci#656). A turn that is not live cannot have anything queued behind it, so
// the count is 0 then: a single pending reply is just the next turn waiting for
// a worker, not a message that was sent into a running chat.
func BotReplyQueuedCount(inspector *asynq.Inspector, chatUUID string) int {
	if inspector == nil || chatUUID == "" {
		return 0
	}
	if !BotReplyIsActive(inspector, chatUUID) {
		return 0
	}
	base := BotReplyTaskID(chatUUID)
	count := 0
	for _, task := range botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID) {
		if task.ID != base {
			count++
		}
	}
	return count
}

// CancelBotReplyTask stops the in-flight bot reply and removes every queued
// reply for the chat, including replies queued behind a live turn.
func CancelBotReplyTask(inspector *asynq.Inspector, chatUUID string) {
	if inspector == nil || chatUUID == "" {
		return
	}
	tasks := botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID)...)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListScheduledTasks, chatUUID)...)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListRetryTasks, chatUUID)...)
	for _, task := range tasks {
		_ = inspector.CancelProcessing(task.ID)
		_ = inspector.DeleteTask(QueueDefault, task.ID)
	}
}

// EnqueueBotReply schedules a bot reply with the default delivery mode: a live
// turn is never cancelled, the reply is queued behind it (ci#656).
func EnqueueBotReply(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return EnqueueBotReplyWithDelivery(client, inspector, payload, BotReplyDeliveryQueued, opts...)
}

// EnqueueBotReplyWithDelivery schedules a bot reply honouring an explicit
// delivery mode (ci#656).
//
//   - queued (default): a live turn keeps running and the reply is queued
//     behind it under a unique task ID, so a message sent into a running chat
//     is answered instead of disappearing. When no turn is live, any queued
//     reply for the chat is replaced by the new one.
//   - interrupt: every reply task of the chat (live turn included) is cancelled
//     and the new reply takes the base task ID, so it starts immediately. Work
//     the stopped turn already persisted (files, commits) is untouched.
func EnqueueBotReplyWithDelivery(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, mode BotReplyDeliveryMode, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return enqueueBotReplyWithDelivery(client, inspector, payload, mode, BotReplyIsActive(inspector, payload.ChatUUID), opts...)
}

// enqueueBotReplyWithDelivery is the liveness-injected core of
// EnqueueBotReplyWithDelivery. turnLive is passed in so the queue behaviour is
// testable without a running worker: a task waiting in the queue is *pending*,
// not *active*, so a unit test cannot otherwise reach the queue-behind case.
func enqueueBotReplyWithDelivery(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, mode BotReplyDeliveryMode, turnLive bool, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is required")
	}

	// Idle and interrupt replies take the stable per-chat task ID (the same key
	// the API tests and the dispatch dedupe expect) after every reply task of
	// the chat has been cleared. Only a reply queued behind a live turn needs a
	// per-message ID, because the live turn keeps the base ID: asynq archives a
	// finished/cancelled task under its ID and a re-enqueue with the same ID
	// fails with "task already exists", which would silently block a queued
	// reply.
	taskID := BotReplyTaskID(payload.ChatUUID)
	switch DecideBotReplyEnqueue(mode, turnLive) {
	case BotReplyDecisionQueueBehind:
		// The live turn keeps running: append the reply behind it under its own
		// per-message task ID instead of clobbering the base ID.
		taskID = botReplyTaskID(payload.ChatUUID, payload.MessageUUID)
		if inspector != nil {
			_ = inspector.DeleteTask(QueueDefault, taskID)
		}
	default:
		// interrupt (stop the live turn now) and replace (nothing is running,
		// the newest message wins) both clear every reply task of the chat,
		// which frees the base ID for the new reply.
		CancelBotReplyTask(inspector, payload.ChatUUID)
	}

	task, err := NewBotReplyTask(payload)
	if err != nil {
		return nil, err
	}

	enqueue := func(id string) (*asynq.TaskInfo, error) {
		enqueueOpts := []asynq.Option{
			asynq.Queue(QueueDefault),
			asynq.TaskID(id),
			asynq.MaxRetry(10),
			asynq.Timeout(BotReplyTimeout()),
			asynq.Retention(0),
		}
		enqueueOpts = append(enqueueOpts, opts...)
		return client.Enqueue(task, enqueueOpts...)
	}
	info, err := enqueue(taskID)
	if err != nil {
		// The stable per-chat ID can still be held by an archived/finished task
		// from an earlier turn; asynq then rejects the re-enqueue. Fall back to a
		// unique ID instead of failing the whole reply. A genuine queue error is
		// returned by the second attempt.
		info, err = enqueue(taskID + ":" + strconv.FormatInt(time.Now().UnixNano(), 36))
	}
	return info, err
}
