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

// botReplyTasksAnyState lists a chat's bot reply tasks in every non-terminal
// queue state: active (running), pending (queued behind another turn or waiting
// for a free worker), scheduled (delayed wake-up) and retry.
func botReplyTasksAnyState(inspector *asynq.Inspector, chatUUID string) []*asynq.TaskInfo {
	if inspector == nil || chatUUID == "" {
		return nil
	}
	tasks := botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID)...)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListScheduledTasks, chatUUID)...)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListRetryTasks, chatUUID)...)
	return tasks
}

// BotReplyQueuedOrActive reports whether a bot reply for the chat is live or
// waiting in the queue. Unlike BotReplyIsActive (running turns only) this also
// counts a reply that has been enqueued but not yet picked up by a worker, which
// is the state a just-dispatched interaction sits in while the queue is busy.
// Callers that must not duplicate scheduled work (eg the agent-dispatch dedupe
// guard, ci#625) use this so two dispatches in quick succession cannot both
// start a full agent.
func BotReplyQueuedOrActive(inspector *asynq.Inspector, chatUUID string) bool {
	return len(botReplyTasksAnyState(inspector, chatUUID)) > 0
}

// CancelBotReplyTask stops the in-flight bot reply and removes every queued
// reply for the chat, including replies queued behind a live turn.
func CancelBotReplyTask(inspector *asynq.Inspector, chatUUID string) {
	for _, task := range botReplyTasksAnyState(inspector, chatUUID) {
		_ = inspector.CancelProcessing(task.ID)
		_ = inspector.DeleteTask(QueueDefault, task.ID)
	}
}

// EnqueueBotReply schedules a bot reply. A live turn is never cancelled: the
// reply is queued behind it under a unique task ID so a message sent into a
// running chat is answered instead of disappearing (ci#656). When no turn is
// live, any queued reply for the chat is replaced by the new one.
func EnqueueBotReply(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is required")
	}

	// Always use a per-message task ID: asynq archives a finished/cancelled task
	// under its ID and a re-enqueue with the same ID fails with "task already
	// exists", which silently blocked every continuation of a finished turn.
	taskID := botReplyTaskID(payload.ChatUUID, payload.MessageUUID)
	if BotReplyIsActive(inspector, payload.ChatUUID) {
		_ = inspector.DeleteTask(QueueDefault, taskID)
	} else {
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
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists") {
		// A retried enqueue can collide with an archived task of the same
		// message; use a unique suffix rather than failing the whole reply.
		info, err = enqueue(taskID + ":" + strconv.FormatInt(time.Now().UnixNano(), 36))
	}
	return info, err
}
