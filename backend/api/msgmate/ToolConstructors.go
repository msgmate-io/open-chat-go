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

func NewToolInitTestToolPassThrough() Tool {
	return NewToolFromDefinition(tooldefs.ToolInitTestToolPassThroughDef)
}

func registerBuiltinTools() {
	registerToolConstructor("get_weather", nil, NewWeatherTool)
	registerToolConstructor("get_current_time", nil, NewCurrentTimeTool)
	registerToolConstructor("get_current_time_confirmed", nil, NewCurrentTimeConfirmedTool)
	registerToolConstructor("get_current_time_confirmed_testing", nil, NewCurrentTimeConfirmedTestingTool)
	registerToolConstructor("get_random_number", nil, NewRandomNumberTool)
	registerToolConstructor("little_world__chat_reply", nil, NewLittleWorldChatReplyTool)
	registerToolConstructor("little_world__get_user_state", nil, NewLittleWorldGetUserStateTool)
	registerToolConstructor("little_world__set_user_searching_state", nil, NewLittleWorldSetUserSearchingStateTool)
	registerToolConstructor("little_world__get_past_messages", nil, NewLittleWorldGetPastMessagesWithUserTool)
	registerToolConstructor("little_world__retrieve_match_overview", nil, NewLittleWorldRetrieveMatchOverviewTool)
	registerToolConstructor("little_world__resolve_match", nil, NewLittleWorldResolveMatchTool)
	registerToolConstructor(
		"rwth_aachen_seminar_tims_auto_paper_include_exclude",
		[]string{"rwth_aachen_seminar_tims_auto_paper_include_exclude_agent"},
		NewRWTHAachenSeminarTimsAutoPaperIncludeExcludeAgent,
	)
	registerToolConstructor("run_callback_function", nil, NewRunCallbackFunctionTool)
	registerToolConstructor("n8n_trigger_workflow_webhook", nil, NewN8NTriggerWorkflowWebhookTool)
	registerToolConstructor("create_confirmable_action_suggestion", nil, NewCreateConfirmableActionSuggestionTool)
	registerToolConstructor("tool_init_test_tool_pass_through", nil, NewToolInitTestToolPassThrough)
}
