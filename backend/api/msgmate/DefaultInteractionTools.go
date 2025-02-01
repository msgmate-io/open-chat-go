package msgmate

import (
	"fmt"
)

// --- Run Callback Function Tool

type RunCallbackFunctionToolInput struct {
	CallbackFunctionID string `json:"callback_function_id"`
}

var RunCallbackFunctionToolDef = ToolDefinition{
	Name:           "run_callback_function",
	Description:    "Run a callback function",
	RequiresInit:   true,
	InputType:      RunCallbackFunctionToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		callbackFunctionID := initData["callback_function_id"].(string)
		callbackFunction, err := GetGlobalMsgmateHandler().GetFunction(callbackFunctionID)
		if err != nil {
			return "", fmt.Errorf("callback function not found: %s", callbackFunctionID)
		}
		callbackFunction.Execute(initData, input.(map[string]interface{}))
		return "", nil
	},
}

func NewRunCallbackFunctionTool() Tool {
	return NewToolFromDefinition(RunCallbackFunctionToolDef)
}
