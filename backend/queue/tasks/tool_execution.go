package tasks

import (
	"backend/api/msgmate"
	"backend/database"
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

func HandleToolExecution(_ context.Context, task *asynq.Task, deps Deps) error {
	if deps.DB == nil {
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
	if err := deps.DB.First(&user, "id = ?", payload.UserID).Error; err != nil {
		return fmt.Errorf("%w: user not found", asynq.SkipRetry)
	}

	if !user.IsAutomated {
		return fmt.Errorf("%w: only bot users can execute tools", asynq.SkipRetry)
	}

	var chat database.Chat
	if err := deps.DB.Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", payload.ChatUUID, user.ID, user.ID).
		First(&chat).Error; err != nil {
		return fmt.Errorf("%w: chat not found or access denied", asynq.SkipRetry)
	}

	toolInitData := database.NewToolInitDataManager(deps.DB).ResolveToolInitData(chat, payload.ToolName)
	dynamicTools := map[string]interface{}{}
	mcpTools := map[string]interface{}{}
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		configData := map[string]interface{}{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
			if raw, ok := configData["dynamic_tools"].(map[string]interface{}); ok {
				dynamicTools = raw
			}
			if raw, ok := configData["mcp_tools"].(map[string]interface{}); ok {
				mcpTools = raw
			}
		}
	}

	toolInstance, dynamicErr := msgmate.GetNewToolInstanceByNameOrSnapshot(payload.ToolName, toolInitData, dynamicTools, mcpTools)
	if dynamicErr != nil {
		failure := ToolExecutionResult{Success: false, Error: dynamicErr.Error()}
		_ = writeResult(task, failure)
		persistTaskResult(deps.DB, task, failure)
		return fmt.Errorf("invalid dynamic tool definition: %w", dynamicErr)
	}
	if toolInstance == nil {
		failure := ToolExecutionResult{
			Success: false,
			Error:   fmt.Sprintf("tool '%s' not found", payload.ToolName),
		}
		_ = writeResult(task, failure)
		persistTaskResult(deps.DB, task, failure)
		return fmt.Errorf("tool '%s' not found", payload.ToolName)
	}

	var (
		toolResult string
		err        error
	)

	if payload.InputParameters != nil {
		toolInput, parseErr := toolInstance.ParseArguments(convertMapToJSON(payload.InputParameters))
		if parseErr != nil {
			failure := ToolExecutionResult{
				Success: false,
				Error:   fmt.Sprintf("invalid tool input parameters: %v", parseErr),
			}
			_ = writeResult(task, failure)
			persistTaskResult(deps.DB, task, failure)
			return fmt.Errorf("invalid tool input parameters: %w", parseErr)
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
		failure := ToolExecutionResult{Success: false, Error: err.Error()}
		_ = writeResult(task, failure)
		persistTaskResult(deps.DB, task, failure)
		return fmt.Errorf("tool execution failed: %w", err)
	}

	success := ToolExecutionResult{Success: true, Result: toolResult}
	persistTaskResult(deps.DB, task, success)
	return writeResult(task, success)
}
