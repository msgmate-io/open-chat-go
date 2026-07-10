package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"backend/workqueue"

	"github.com/hibiken/asynq"
	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func HandleEmailAutomation(_ context.Context, task *asynq.Task, deps Deps) error {
	if deps.DB == nil {
		return fmt.Errorf("%w: database unavailable", asynq.SkipRetry)
	}
	var payload workqueue.EmailAutomationPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: invalid payload: %v", asynq.SkipRetry, err)
	}
	payload.AutomationName = strings.TrimSpace(payload.AutomationName)
	payload.ReceiverUserUUID = strings.TrimSpace(payload.ReceiverUserUUID)
	payload.ReceiverEmail = strings.TrimSpace(strings.ToLower(payload.ReceiverEmail))
	payload.ReceiverName = strings.TrimSpace(payload.ReceiverName)
	if payload.AutomationName == "" {
		return fmt.Errorf("%w: automation_name is required", asynq.SkipRetry)
	}
	if payload.ReceiverUserUUID == "" && payload.ReceiverEmail == "" {
		return fmt.Errorf("%w: receiver_user_uuid or receiver_email is required", asynq.SkipRetry)
	}
	def, ok := integrationinterface.Get("emails")
	if !ok {
		return fmt.Errorf("%w: emails integration not registered", asynq.SkipRetry)
	}
	fn, ok := def.Functions["send_automation_email"]
	if !ok || fn == nil {
		return fmt.Errorf("%w: send_automation_email not registered", asynq.SkipRetry)
	}
	requestPayload := map[string]interface{}{
		"db":                 deps.DB,
		"automation_name":    payload.AutomationName,
		"receiver_user_uuid": payload.ReceiverUserUUID,
		"receiver_email":     payload.ReceiverEmail,
		"receiver_name":      payload.ReceiverName,
		"template_values":    payload.TemplateValues,
	}
	_, err := fn(context.Background(), requestPayload)
	if err != nil {
		return err
	}
	return nil
}
