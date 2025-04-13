package tools

import "time"

type CurrentTimeToolInput struct{}

const CurrentTimeConfirmedTestingFixedRFC3339 = "2026-01-02T03:04:05Z"

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
	FunctionName:                   "get_current_time",
	Description:                    "Return the current server time in RFC3339 format.",
	RequiresInit:                   false,
	RequiresConfirmation:           true,
	StopOnFirstConfirmableToolCall: true,
	ConfirmationBlockMessage:       "Confirmation Message was send to user. Tool execution is pending user confirmation. Do not assume or fabricate a result.",
	InputType:                      CurrentTimeToolInput{},
	RequiredParams:                 []string{},
	Parameters:                     map[string]interface{}{},
	RunFunction: func(_ interface{}, _ map[string]interface{}) (string, error) {
		return time.Now().Format(time.RFC3339), nil
	},
}

var CurrentTimeConfirmedTestingToolDef = func() ToolDefinition {
	def := CurrentTimeConfirmedToolDef
	originalRun := def.RunFunction

	def.Name = "get_current_time_confirmed_testing"
	def.FunctionName = "get_current_time_confirmed_testing"
	def.Description = "Testing-only variant of get_current_time_confirmed that returns a fixed mocked RFC3339 timestamp after user confirmation."
	def.RunFunction = func(input interface{}, init map[string]interface{}) (string, error) {
		if _, err := originalRun(input, init); err != nil {
			return "", err
		}
		return CurrentTimeConfirmedTestingFixedRFC3339, nil
	}

	return def
}()
