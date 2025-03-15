package queue

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
	"gorm.io/gorm"
)

type Processor struct {
	DB          *gorm.DB
	BackendHost string
	WSHandler   *wsapi.WebSocketHandler
}

func (p *Processor) NewServeMux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeToolExecution, p.handleToolExecution)
	mux.HandleFunc(workqueue.TypeBotReply, p.handleBotReply)
	return mux
}

func (p *Processor) handleToolExecution(_ context.Context, task *asynq.Task) error {
	if p.DB == nil {
		return fmt.Errorf("%w: database unavailable", asynq.SkipRetry)
	}

	var payload ToolExecutionPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: invalid payload: %v", asynq.SkipRetry, err)
	}

	if payload.ChatUUID == "" || payload.ToolName == "" || payload.UserID == 0 {
		return fmt.Errorf("%w: chat_uuid, tool_name and user_id are required", asynq.SkipRetry)
	}

	var user database.User
	if err := p.DB.First(&user, "id = ?", payload.UserID).Error; err != nil {
		return fmt.Errorf("%w: user not found", asynq.SkipRetry)
	}

	if !user.IsAutomated {
		return fmt.Errorf("%w: only bot users can execute tools", asynq.SkipRetry)
	}

	var chat database.Chat
	if err := p.DB.Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", payload.ChatUUID, user.ID, user.ID).
		First(&chat).Error; err != nil {
		return fmt.Errorf("%w: chat not found or access denied", asynq.SkipRetry)
	}

	toolInitData := make(map[string]interface{})
	if chat.SharedConfig != nil && chat.SharedConfig.ConfigData != nil {
		var configData map[string]interface{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
			if toolInit, exists := configData["tool_init"]; exists {
				if toolInitMap, ok := toolInit.(map[string]interface{}); ok {
					if initData, exists := toolInitMap[payload.ToolName]; exists {
						if initDataMap, ok := initData.(map[string]interface{}); ok {
							toolInitData = initDataMap
						}
					}
				}
			}
		}
	}

	toolInstance := msgmate.GetNewToolInstanceByName(payload.ToolName, toolInitData)
	if toolInstance == nil {
		return p.writeResult(task, ToolExecutionResult{
			Success: false,
			Error:   fmt.Sprintf("tool '%s' not found", payload.ToolName),
		})
	}

	var (
		toolResult string
		err        error
	)

	if payload.InputParameters != nil {
		toolInput, parseErr := toolInstance.ParseArguments(convertMapToJSON(payload.InputParameters))
		if parseErr != nil {
			return p.writeResult(task, ToolExecutionResult{
				Success: false,
				Error:   fmt.Sprintf("invalid tool input parameters: %v", parseErr),
			})
		}
		toolResult, err = toolInstance.RunTool(toolInput)
	} else {
		toolInput, parseErr := toolInstance.ParseArguments("{}")
		if parseErr != nil {
			toolResult, err = toolInstance.RunTool(nil)
		} else {
			toolResult, err = toolInstance.RunTool(toolInput)
		}
	}

	if err != nil {
		return p.writeResult(task, ToolExecutionResult{Success: false, Error: err.Error()})
	}

	return p.writeResult(task, ToolExecutionResult{Success: true, Result: toolResult})
}

func (p *Processor) handleBotReply(ctx context.Context, task *asynq.Task) error {
	if p.DB == nil {
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
	if err := p.DB.First(&botUser, "id = ?", payload.BotUserID).Error; err != nil {
		return fmt.Errorf("%w: bot user not found", asynq.SkipRetry)
	}
	if !botUser.IsAutomated {
		return fmt.Errorf("%w: receiver is not an automated user", asynq.SkipRetry)
	}

	var incomingMessage database.Message
	if err := p.DB.Where("uuid = ?", payload.MessageUUID).First(&incomingMessage).Error; err != nil {
		return fmt.Errorf("%w: source message not found", asynq.SkipRetry)
	}

	var senderUser database.User
	if err := p.DB.First(&senderUser, "id = ?", incomingMessage.SenderId).Error; err != nil {
		return fmt.Errorf("%w: sender not found", asynq.SkipRetry)
	}

	token := uuid.NewString()
	session := database.Session{
		UserId: botUser.ID,
		Token:  token,
		Expiry: time.Now().Add(15 * time.Minute),
	}
	if err := p.DB.Create(&session).Error; err != nil {
		return fmt.Errorf("%w: failed to create bot session", asynq.SkipRetry)
	}
	defer p.DB.Where("token = ?", token).Delete(&database.Session{})

	host := p.BackendHost
	if host == "" {
		host = "http://127.0.0.1:1984"
	}

	ocClient := client.NewClient(host)
	ocClient.SetSessionId(token)
	ocClient.User = botUser

	wsHandler := p.WSHandler
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
			return fmt.Errorf("bot reply interrupted: %w", asynq.SkipRetry)
		}
		return p.writeResult(task, ToolExecutionResult{Success: false, Error: err.Error()})
	}

	return p.writeResult(task, ToolExecutionResult{Success: true, Result: "bot reply generated"})
}

func (p *Processor) writeResult(task *asynq.Task, result ToolExecutionResult) error {
	if task.ResultWriter() == nil {
		return nil
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}

	_, err = task.ResultWriter().Write(resultBytes)
	return err
}

func convertMapToJSON(data map[string]interface{}) string {
	if data == nil {
		return "{}"
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "{}"
	}

	return string(jsonBytes)
}
