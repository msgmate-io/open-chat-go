package workqueue

import (
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

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
		asynq.Timeout(5 * time.Minute),
		asynq.Retention(0),
	}
	enqueueOpts = append(enqueueOpts, opts...)

	return client.Enqueue(task, enqueueOpts...)
}
