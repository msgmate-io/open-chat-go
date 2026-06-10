package tools

import "fmt"

type RunCallbackFunctionToolInput struct {
	CallbackFunctionID string `json:"callback_function_id"`
}

var RunCallbackFunctionToolDef = ToolDefinition{
	Name:           "run_callback_function",
	Description:    "Run a callback function.",
	RequiresInit:   true,
	InputType:      RunCallbackFunctionToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		inputMap, ok := input.(map[string]interface{})
		if !ok {
			inputMap = map[string]interface{}{}
		}
		if RunCallbackExecutor == nil {
			return "", fmt.Errorf("callback executor is not configured")
		}
		if err := RunCallbackExecutor(initData, inputMap); err != nil {
			return "", err
		}
		return "", nil
	},
}
