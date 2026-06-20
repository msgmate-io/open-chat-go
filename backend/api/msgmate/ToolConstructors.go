package msgmate

import (
	tooldefs "backend/api/msgmate/tools"
	"fmt"
)

func init() {
	tooldefs.RunCallbackExecutor = func(initData map[string]interface{}, input map[string]interface{}) error {
		callbackFunctionID, _ := initData["callback_function_id"].(string)
		callbackFunction, err := GetGlobalMsgmateHandler().GetFunction(callbackFunctionID)
		if err != nil {
			return fmt.Errorf("callback function not found: %s", callbackFunctionID)
		}
		callbackFunction.Execute(initData, input)
		return nil
	}
}

func NewWeatherTool() Tool {
	return NewToolFromDefinition(tooldefs.WeatherToolDef)
}

func NewCurrentTimeTool() Tool {
	return NewToolFromDefinition(tooldefs.CurrentTimeToolDef)
}

func NewCurrentTimeConfirmedTool() Tool {
	return NewToolFromDefinition(tooldefs.CurrentTimeConfirmedToolDef)
}

func NewCurrentTimeConfirmedTestingTool() Tool {
	return NewToolFromDefinition(tooldefs.CurrentTimeConfirmedTestingToolDef)
}

func NewRandomNumberTool() Tool {
	return NewToolFromDefinition(tooldefs.RandomNumberToolDef)
}

func NewLittleWorldChatReplyTool() Tool {
	return NewToolFromDefinition(tooldefs.LittleWorldChatReplyToolDef)
}

func NewLittleWorldGetUserStateTool() Tool {
	return NewToolFromDefinition(tooldefs.LittleWorldGetUserStateToolDef)
}

func NewLittleWorldSetUserSearchingStateTool() Tool {
	return NewToolFromDefinition(tooldefs.LittleWorldSetUserSearchingStateToolDef)
}

func NewLittleWorldGetPastMessagesWithUserTool() Tool {
	return NewToolFromDefinition(tooldefs.LittleWorldGetPastMessagesWithUserToolDef)
}

func NewLittleWorldRetrieveMatchOverviewTool() Tool {
	return NewToolFromDefinition(tooldefs.LittleWorldRetrieveMatchOverviewToolDef)
}

func NewLittleWorldResolveMatchTool() Tool {
	return NewToolFromDefinition(tooldefs.LittleWorldResolveMatchToolDef)
}

func NewRWTHAachenSeminarTimsAutoPaperIncludeExcludeAgent() Tool {
	return NewToolFromDefinition(tooldefs.RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentDef)
}

func NewRunCallbackFunctionTool() Tool {
	return NewToolFromDefinition(tooldefs.RunCallbackFunctionToolDef)
}

func NewN8NTriggerWorkflowWebhookTool() Tool {
	return NewToolFromDefinition(tooldefs.N8NTriggerWorkflowWebhookToolDef)
}

func NewCreateConfirmableActionSuggestionTool() Tool {
	return NewToolFromDefinition(tooldefs.CreateConfirmableActionSuggestionToolDef)
}
