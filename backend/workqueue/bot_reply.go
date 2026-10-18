package workqueue

import (
	"fmt"
	"os"
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

// CancelBotReplyTask stops an in-flight bot reply and removes a queued one, if any.
func CancelBotReplyTask(inspector *asynq.Inspector, chatUUID string) {
	if inspector == nil || chatUUID == "" {
		return
	}

	taskID := BotReplyTaskID(chatUUID)
	_ = inspector.CancelProcessing(taskID)
	_ = inspector.DeleteTask(QueueDefault, taskID)
}

// EnqueueBotReply schedules a bot reply, cancelling any existing reply task for the chat first.
func EnqueueBotReply(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is required")
	}

	CancelBotReplyTask(inspector, payload.ChatUUID)

	task, err := NewBotReplyTask(payload)
	if err != nil {
		return nil, err
	}

	enqueueOpts := []asynq.Option{
		asynq.Queue(QueueDefault),
		asynq.TaskID(BotReplyTaskID(payload.ChatUUID)),
		asynq.MaxRetry(10),
		asynq.Timeout(BotReplyTimeout()),
		asynq.Retention(0),
	}
	enqueueOpts = append(enqueueOpts, opts...)

	return client.Enqueue(task, enqueueOpts...)
}
