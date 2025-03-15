package workqueue

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

const (
	QueueDefault = "default"
	TypeBotReply = "bot:reply"
)

type BotReplyPayload struct {
	ChatUUID    string `json:"chat_uuid"`
	MessageUUID string `json:"message_uuid"`
	BotUserID   uint   `json:"bot_user_id"`
}

func NewBotReplyTask(payload BotReplyPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return asynq.NewTask(TypeBotReply, payloadBytes), nil
}
