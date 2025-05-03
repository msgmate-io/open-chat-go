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
	registerToolConstructor("run_callback_function", nil, NewRunCallbackFunctionTool)
	registerToolConstructor("n8n_trigger_workflow_webhook", nil, NewN8NTriggerWorkflowWebhookTool)
	registerToolConstructor("create_confirmable_action_suggestion", nil, NewCreateConfirmableActionSuggestionTool)
	registerToolConstructor("tool_init_test_tool_pass_through", nil, NewToolInitTestToolPassThrough)
}
