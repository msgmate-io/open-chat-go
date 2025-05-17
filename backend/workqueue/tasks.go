package workqueue

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

const (
	QueueDefault        = "default"
	TypeBotReply        = "bot:reply"
	TypeEmailAutomation = "emails:automation"
)

type BotReplyPayload struct {
	ChatUUID    string `json:"chat_uuid"`
	MessageUUID string `json:"message_uuid"`
	BotUserID   uint   `json:"bot_user_id"`
}

type EmailAutomationPayload struct {
	AutomationName   string            `json:"automation_name"`
	ReceiverUserUUID string            `json:"receiver_user_uuid,omitempty"`
	ReceiverEmail    string            `json:"receiver_email,omitempty"`
	ReceiverName     string            `json:"receiver_name,omitempty"`
	TemplateValues   map[string]string `json:"template_values,omitempty"`
}

func NewBotReplyTask(payload BotReplyPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return asynq.NewTask(TypeBotReply, payloadBytes), nil
}

func NewEmailAutomationTask(payload EmailAutomationPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return asynq.NewTask(TypeEmailAutomation, payloadBytes), nil
}
