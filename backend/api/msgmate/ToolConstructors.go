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
	// ci#680: back the `coder_context` tool with the core's per-chat context
	// usage report (the tools package cannot import the msgmate package it is
	// embedded in).
	tooldefs.ContextUsageLookup = contextUsageLookup
	// ci#724: back the `coder_schedule` tool with the core's in-turn scheduler,
	// so a lead agent can start a later turn on its own chat without a GitHub
	// workflow or a CronJob.
	tooldefs.CoderScheduleRunnerProvider = coderScheduleToolRunner
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

func NewRandomNumberSeededTool() Tool {
	return NewToolFromDefinition(tooldefs.RandomNumberSeededToolDef)
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

func NewWebFetchTool() Tool {
	return NewToolFromDefinition(tooldefs.WebFetchToolDef)
}

func NewWebSearchTool() Tool {
	return NewToolFromDefinition(tooldefs.WebSearchToolDef)
}

// NewCoderContextTool is the ci#680 context/token usage report tool.
func NewCoderContextTool() Tool {
	return NewToolFromDefinition(tooldefs.CoderContextToolDef)
}

// NewCoderScheduleTool is the ci#724 self-scheduling tool: it lets a lead agent
// queue a later turn of its own chat instead of relying on an external CronJob.
func NewCoderScheduleTool() Tool {
	return NewToolFromDefinition(tooldefs.CoderScheduleToolDef)
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
	registerToolConstructor("get_random_number_seeded", nil, NewRandomNumberSeededTool)
	registerToolConstructor("run_callback_function", nil, NewRunCallbackFunctionTool)
	registerToolConstructor("n8n_trigger_workflow_webhook", nil, NewN8NTriggerWorkflowWebhookTool)
	registerToolConstructor("create_confirmable_action_suggestion", nil, NewCreateConfirmableActionSuggestionTool)
	registerToolConstructor("tool_init_test_tool_pass_through", nil, NewToolInitTestToolPassThrough)
	registerToolConstructor("web_fetch", nil, NewWebFetchTool)
	registerToolConstructor("web_search", nil, NewWebSearchTool)
}
