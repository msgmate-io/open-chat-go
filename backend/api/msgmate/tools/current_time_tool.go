package tools

import "time"

type CurrentTimeToolInput struct{}

var CurrentTimeToolDef = ToolDefinition{
	Name:           "get_current_time",
	Description:    "Return the current server time in RFC3339 format.",
	RequiresInit:   false,
	InputType:      CurrentTimeToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(_ interface{}, _ map[string]interface{}) (string, error) {
		return time.Now().Format(time.RFC3339), nil
	},
}
