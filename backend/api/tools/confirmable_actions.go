package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
)

type ConfirmableActionExecuteRequest struct {
	Input                map[string]interface{} `json:"input,omitempty"`
	ContinueAfterExecute *bool                  `json:"continue_after_execute,omitempty"`
}

type ConfirmableActionExecuteResponse struct {
	Success              bool                   `json:"success"`
	Status               string                 `json:"status"`
	ActionID             string                 `json:"action_id"`
	TargetTool           string                 `json:"target_tool_name"`
	ToolResult           string                 `json:"tool_result,omitempty"`
	Error                string                 `json:"error,omitempty"`
	ContinueAfterExecute bool                   `json:"continue_after_execute"`
	ContinuationQueued   bool                   `json:"continuation_queued,omitempty"`
	UpdatedAction        map[string]interface{} `json:"updated_action,omitempty"`
}

func getToolInitForChat(DB *gorm.DB, chat database.Chat, toolName string) map[string]interface{} {
	return database.NewToolInitDataManager(DB).ResolveToolInitData(chat, toolName)
}

func findBotAndReceiver(chat database.Chat, user database.User) (uint, uint) {
	if chat.User1.IsAutomated {
		return chat.User1.ID, chat.User2.ID
	}
	if chat.User2.IsAutomated {
		return chat.User2.ID, chat.User1.ID
	}
	if chat.User1.ID == user.ID {
		return chat.User2.ID, chat.User1.ID
	}
	return chat.User1.ID, chat.User2.ID
}

func updateSourceMessageToolCallResult(tx *gorm.DB, sourceMessage database.Message, actionID, toolResult string) error {
	if sourceMessage.ToolCalls == nil || len(*sourceMessage.ToolCalls) == 0 {
		return nil
	}

	updatedToolCalls := make([]json.RawMessage, 0, len(*sourceMessage.ToolCalls))
	updated := false
	for _, rawToolCall := range *sourceMessage.ToolCalls {
		var toolCall map[string]interface{}
		if err := json.Unmarshal(rawToolCall, &toolCall); err != nil {
			updatedToolCalls = append(updatedToolCalls, rawToolCall)
			continue
		}

		toolCallID, _ := toolCall["id"].(string)
		if toolCallID == actionID {
			toolCall["result"] = toolResult
			if confirmationMeta, ok := toolCall["confirmation"].(map[string]interface{}); ok {
				confirmationMeta["status"] = "executed"
				confirmationMeta["executed"] = true
				toolCall["confirmation"] = confirmationMeta
			}
			updated = true
		}

		encoded, err := json.Marshal(toolCall)
		if err != nil {
			updatedToolCalls = append(updatedToolCalls, rawToolCall)
			continue
		}
		updatedToolCalls = append(updatedToolCalls, encoded)
	}

	if !updated {
		return nil
	}

	encodedToolCalls, err := json.Marshal(updatedToolCalls)
	if err != nil {
		return err
	}

	return tx.Model(&database.Message{}).Where("id = ?", sourceMessage.ID).Update("tool_calls", string(encodedToolCalls)).Error
}

func (h *ToolsHandler) ExecuteConfirmableAction(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := r.PathValue("chat_uuid")
	messageUUID := r.PathValue("message_uuid")
	actionID := r.PathValue("action_id")
	if chatUUID == "" || messageUUID == "" || actionID == "" {
		http.Error(w, "Invalid confirmable action path", http.StatusBadRequest)
		return
	}

	var req ConfirmableActionExecuteRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	var chat database.Chat
	if err := DB.Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, user.ID, user.ID).
		First(&chat).Error; err != nil {
		http.Error(w, "Chat not found or access denied", http.StatusNotFound)
		return
	}

	var sourceMessage database.Message
	if err := DB.Where("uuid = ? AND chat_id = ?", messageUUID, chat.ID).First(&sourceMessage).Error; err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	messageMeta := map[string]interface{}{}
	if len(sourceMessage.MetaData) > 0 {
		_ = json.Unmarshal(sourceMessage.MetaData, &messageMeta)
	}
	actionsRaw, ok := messageMeta["confirmable_actions"].([]interface{})
	if !ok || len(actionsRaw) == 0 {
		http.Error(w, "No confirmable actions in message metadata", http.StatusBadRequest)
		return
	}

	actionIndex := -1
	var selectedAction map[string]interface{}
	for i, rawAction := range actionsRaw {
		action, ok := rawAction.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := action["action_id"].(string); id == actionID {
			actionIndex = i
			selectedAction = action
			break
		}
	}
	if actionIndex < 0 {
		http.Error(w, "Confirmable action not found", http.StatusNotFound)
		return
	}

	status, _ := selectedAction["status"].(string)
	if status != "" && status != "pending" {
		http.Error(w, "Action already handled", http.StatusConflict)
		return
	}

	targetToolName, _ := selectedAction["target_tool_name"].(string)
	if targetToolName == "" {
		http.Error(w, "Missing target tool name", http.StatusBadRequest)
		return
	}

	inputParams := map[string]interface{}{}
	if suggested, ok := selectedAction["input"].(map[string]interface{}); ok {
		inputParams = suggested
	}
	if req.Input != nil {
		inputParams = req.Input
	}

	continueAfterExecute := false
	if configuredContinue, ok := selectedAction["continue_after_execute"].(bool); ok {
		continueAfterExecute = configuredContinue
	}
	if req.ContinueAfterExecute != nil {
		continueAfterExecute = *req.ContinueAfterExecute
	}

	toolInitData := getToolInitForChat(DB, chat, targetToolName)
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
	toolInstance, dynamicErr := msgmate.GetNewToolInstanceByNameOrSnapshot(targetToolName, toolInitData, dynamicTools, mcpTools)
	if dynamicErr != nil {
		http.Error(w, fmt.Sprintf("Invalid dynamic tool: %v", dynamicErr), http.StatusBadRequest)
		return
	}
	if toolInstance == nil {
		http.Error(w, fmt.Sprintf("Tool '%s' not found", targetToolName), http.StatusNotFound)
		return
	}

	inputBytes, _ := json.Marshal(inputParams)
	toolInput, err := toolInstance.ParseArguments(string(inputBytes))
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid action input: %v", err), http.StatusBadRequest)
		return
	}

	toolResult, execErr := toolInstance.RunTool(toolInput)
	now := time.Now().UTC().Format(time.RFC3339)
	selectedAction["approved_by"] = user.UUID
	selectedAction["approved_at"] = now
	selectedAction["input"] = inputParams
	selectedAction["continue_after_execute"] = continueAfterExecute

	response := ConfirmableActionExecuteResponse{
		Success:              execErr == nil,
		ActionID:             actionID,
		TargetTool:           targetToolName,
		ContinueAfterExecute: continueAfterExecute,
	}

	if execErr != nil {
		selectedAction["status"] = "failed"
		selectedAction["execution_error"] = execErr.Error()
		response.Status = "failed"
		response.Error = execErr.Error()
	} else {
		selectedAction["status"] = "executed"
		selectedAction["executed_at"] = now
		selectedAction["result"] = toolResult
		response.Status = "executed"
		response.ToolResult = toolResult
	}

	actionsRaw[actionIndex] = selectedAction
	messageMeta["confirmable_actions"] = actionsRaw
	updatedMetaBytes, _ := json.Marshal(messageMeta)

	botUserID, humanUserID := findBotAndReceiver(chat, *user)
	continuationMessageUUID := ""

	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.Message{}).Where("id = ?", sourceMessage.ID).Update("meta_data", updatedMetaBytes).Error; err != nil {
			return err
		}
		if execErr == nil {
			if err := updateSourceMessageToolCallResult(tx, sourceMessage, actionID, toolResult); err != nil {
				return err
			}
		}

		eventRequestedMeta := map[string]interface{}{
			"finished":    true,
			"event_type":  "confirmable_action_execute",
			"event_phase": "requested",
			"confirmable_action_execution": map[string]interface{}{
				"action_id":              actionID,
				"target_tool_name":       targetToolName,
				"approved_by":            user.UUID,
				"approved_at":            now,
				"source_message_uuid":    sourceMessage.UUID,
				"continue_after_execute": continueAfterExecute,
			},
		}
		eventRequestedMetaBytes, _ := json.Marshal(eventRequestedMeta)
		eventRequestedText := ""
		eventRequestedMessage := database.Message{
			ChatId:     chat.ID,
			SenderId:   humanUserID,
			ReceiverId: botUserID,
			DataType:   "event",
			Text:       &eventRequestedText,
			MetaData:   eventRequestedMetaBytes,
		}
		if err := tx.Create(&eventRequestedMessage).Error; err != nil {
			return err
		}
		if continueAfterExecute && execErr == nil {
			continuationMessageUUID = eventRequestedMessage.UUID
		}

		if execErr != nil {
			eventFailedMeta := map[string]interface{}{
				"finished":    true,
				"event_type":  "confirmable_action_execute",
				"event_phase": "failed",
				"confirmable_action_execution": map[string]interface{}{
					"action_id":              actionID,
					"target_tool_name":       targetToolName,
					"approved_by":            user.UUID,
					"approved_at":            now,
					"source_message_uuid":    sourceMessage.UUID,
					"continue_after_execute": continueAfterExecute,
					"error":                  execErr.Error(),
				},
			}
			eventFailedMetaBytes, _ := json.Marshal(eventFailedMeta)
			eventFailedText := fmt.Sprintf("Confirmed action `%s` failed.", targetToolName)
			eventFailedMessage := database.Message{
				ChatId:     chat.ID,
				SenderId:   botUserID,
				ReceiverId: humanUserID,
				DataType:   "event",
				Text:       &eventFailedText,
				MetaData:   eventFailedMetaBytes,
			}
			if err := tx.Create(&eventFailedMessage).Error; err != nil {
				return err
			}
			if err := tx.Model(&chat).Update("latest_message_id", eventFailedMessage.ID).Error; err != nil {
				return err
			}
			return nil
		}

		messageText := fmt.Sprintf("Confirmed action `%s` executed.", targetToolName)
		if toolResult != "" {
			messageText = fmt.Sprintf("Confirmed action `%s` executed.\n\n%s", targetToolName, toolResult)
		}

		execMeta := map[string]interface{}{
			"finished":    true,
			"event_type":  "confirmable_action_execute",
			"event_phase": "completed",
			"confirmable_action_execution": map[string]interface{}{
				"action_id":              actionID,
				"target_tool_name":       targetToolName,
				"approved_by":            user.UUID,
				"approved_at":            now,
				"source_message_uuid":    sourceMessage.UUID,
				"continue_after_execute": continueAfterExecute,
			},
		}
		execMetaBytes, _ := json.Marshal(execMeta)

		toolCallRepr := []map[string]interface{}{{
			"id":        actionID,
			"name":      targetToolName,
			"arguments": inputParams,
			"result":    toolResult,
		}}
		toolCallBytes, _ := json.Marshal(toolCallRepr)
		var toolCalls []json.RawMessage
		_ = json.Unmarshal(toolCallBytes, &toolCalls)

		msg := database.Message{
			ChatId:     chat.ID,
			SenderId:   botUserID,
			ReceiverId: humanUserID,
			DataType:   "event",
			Text:       &messageText,
			MetaData:   execMetaBytes,
			ToolCalls:  &toolCalls,
		}

		if err := tx.Create(&msg).Error; err != nil {
			return err
		}
		if err := tx.Model(&chat).Update("latest_message_id", msg.ID).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		http.Error(w, "Failed to persist confirmable action state", http.StatusInternalServerError)
		return
	}

	if continueAfterExecute && execErr == nil && continuationMessageUUID != "" {
		queueClient, clientErr := util.GetAsynqClient(r)
		queueInspector, inspectorErr := util.GetAsynqInspector(r)
		if clientErr == nil && inspectorErr == nil {
			if _, enqueueErr := workqueue.EnqueueBotReply(queueClient, queueInspector, workqueue.BotReplyPayload{
				ChatUUID:    chatUUID,
				MessageUUID: continuationMessageUUID,
				BotUserID:   botUserID,
			}); enqueueErr == nil {
				response.ContinuationQueued = true
			}
		}
	}

	response.UpdatedAction = selectedAction
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
