package queue

import (
	"backend/api/msgmate"
	"backend/database"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type Processor struct {
	DB *gorm.DB
}

func (p *Processor) NewServeMux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeToolExecution, p.handleToolExecution)
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

	if !isBotUser(user.Name) {
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

func isBotUser(userName string) bool {
	botNames := []string{"signal", "bot", "msgmate"}
	normalizedUserName := strings.ToLower(userName)

	for _, botName := range botNames {
		if normalizedUserName == botName {
			return true
		}
	}

	return false
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
