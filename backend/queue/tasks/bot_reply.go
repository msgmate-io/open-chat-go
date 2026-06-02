package tasks

import (
	"backend/api/msgmate"
	wsapi "backend/api/websocket"
	"backend/client"
	"backend/database"
	"backend/workqueue"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// @doc:open-chat-hal-agent-logic
// Open-Chat HAL agent logic runs as the `bot:reply` async task. It validates
// the bot/sender/message context, creates a short-lived bot session, rebuilds
// websocket-style message payload (including attachments metadata), and then
// calls the AI handler to generate and persist/send the bot response.
func HandleBotReply(ctx context.Context, task *asynq.Task, deps Deps) error {
	if deps.DB == nil {
		return fmt.Errorf("%w: database unavailable", asynq.SkipRetry)
	}

	var payload workqueue.BotReplyPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: invalid payload: %v", asynq.SkipRetry, err)
	}

	if payload.ChatUUID == "" || payload.MessageUUID == "" || payload.BotUserID == 0 {
		return fmt.Errorf("%w: chat_uuid, message_uuid and bot_user_id are required", asynq.SkipRetry)
	}

	var botUser database.User
	if err := deps.DB.First(&botUser, "id = ?", payload.BotUserID).Error; err != nil {
		return fmt.Errorf("%w: bot user not found", asynq.SkipRetry)
	}
	if !botUser.IsAutomated {
		return fmt.Errorf("%w: receiver is not an automated user", asynq.SkipRetry)
	}

	var incomingMessage database.Message
	if err := deps.DB.Where("uuid = ?", payload.MessageUUID).First(&incomingMessage).Error; err != nil {
		return fmt.Errorf("%w: source message not found", asynq.SkipRetry)
	}

	var senderUser database.User
	if err := deps.DB.First(&senderUser, "id = ?", incomingMessage.SenderId).Error; err != nil {
		return fmt.Errorf("%w: sender not found", asynq.SkipRetry)
	}

	token := uuid.NewString()
	session := database.Session{
		UserId: botUser.ID,
		Token:  token,
		Expiry: time.Now().Add(15 * time.Minute),
	}
	if err := deps.DB.Create(&session).Error; err != nil {
		return fmt.Errorf("%w: failed to create bot session", asynq.SkipRetry)
	}
	defer deps.DB.Where("token = ?", token).Delete(&database.Session{})

	host := deps.BackendHost
	if host == "" {
		host = "http://127.0.0.1:1984"
	}

	ocClient := client.NewClient(host)
	ocClient.SetSessionId(token)
	ocClient.User = botUser

	wsHandler := deps.WSHandler
	if wsHandler == nil {
		wsHandler = wsapi.NewWebSocketHandler()
	}

	botContext := &msgmate.BotContext{
		Client:       ocClient,
		BotUser:      botUser,
		WSHandler:    wsHandler,
		ChatCanceler: msgmate.NewChatCanceler(),
	}

	message := wsapi.NewMessage{Type: "new_message"}
	message.Content.ChatUUID = payload.ChatUUID
	message.Content.SenderUUID = senderUser.UUID
	if incomingMessage.Text != nil {
		message.Content.Text = *incomingMessage.Text
	}
	if incomingMessage.Reasoning != nil {
		message.Content.Reasoning = *incomingMessage.Reasoning
	}

	if len(incomingMessage.MetaData) > 0 {
		var meta map[string]interface{}
		if err := json.Unmarshal(incomingMessage.MetaData, &meta); err == nil {
			message.Content.MetaData = &meta
			if rawAttachments, ok := meta["attachments"].([]interface{}); ok {
				attachments := make([]wsapi.FileAttachment, 0, len(rawAttachments))
				for _, rawAttachment := range rawAttachments {
					attachmentMap, ok := rawAttachment.(map[string]interface{})
					if !ok {
						continue
					}
					attachment := wsapi.FileAttachment{}
					if fileID, ok := attachmentMap["file_id"].(string); ok {
						attachment.FileID = fileID
					}
					if mimeType, ok := attachmentMap["mime_type"].(string); ok {
						attachment.MimeType = mimeType
					}
					attachments = append(attachments, attachment)
				}
				if len(attachments) > 0 {
					message.Content.Attachments = &attachments
				}
			}
		}
	}

	aiHandler := msgmate.NewAIHandler(botContext)
	if err := aiHandler.GenerateResponse(ctx, message); err != nil {
		if errors.Is(err, context.Canceled) {
			persistTaskResult(deps.DB, task, ToolExecutionResult{Success: false, Error: err.Error()})
			return fmt.Errorf("bot reply interrupted: %w", asynq.SkipRetry)
		}
		failure := ToolExecutionResult{Success: false, Error: err.Error()}
		_ = writeResult(task, failure)
		persistTaskResult(deps.DB, task, failure)
		return fmt.Errorf("bot reply generation failed: %w", err)
	}

	success := ToolExecutionResult{Success: true, Result: "bot reply generated"}
	persistTaskResult(deps.DB, task, success)
	return writeResult(task, success)
}
