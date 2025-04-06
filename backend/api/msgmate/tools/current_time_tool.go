package tools

import "time"

type CurrentTimeToolInput struct{}

var CurrentTimeToolDef = ToolDefinition{
	Name:                 "get_current_time",
	Description:          "Return the current server time in RFC3339 format.",
	RequiresInit:         false,
	RequiresConfirmation: false,
	InputType:            CurrentTimeToolInput{},
	RequiredParams:       []string{},
	Parameters:           map[string]interface{}{},
	RunFunction: func(_ interface{}, _ map[string]interface{}) (string, error) {
		return time.Now().Format(time.RFC3339), nil
	},
}

var CurrentTimeConfirmedToolDef = ToolDefinition{
	Name:                           "get_current_time_confirmed",
	Description:                    "Return the current server time in RFC3339 format after user confirmation.",
	RequiresInit:                   false,
	RequiresConfirmation:           true,
	StopOnFirstConfirmableToolCall: true,
	ConfirmationBlockMessage:       "Tool execution is pending user confirmation. Do not assume or fabricate a result.",
	InputType:                      CurrentTimeToolInput{},
	RequiredParams:                 []string{},
	Parameters:                     map[string]interface{}{},
	RunFunction: func(_ interface{}, _ map[string]interface{}) (string, error) {
		return time.Now().Format(time.RFC3339), nil
	},
}
