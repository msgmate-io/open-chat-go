package tools

import "fmt"

type ToolInitTestToolPassThroughInput struct{}

var ToolInitTestToolPassThroughDef = ToolDefinition{
	Name:           "tool_init_test_tool_pass_through",
	Description:    "Testing-only tool that returns the configured tool_init pass-through value.",
	RequiresInit:   true,
	InputType:      ToolInitTestToolPassThroughInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(_ interface{}, init map[string]interface{}) (string, error) {
		if init == nil {
			return "", fmt.Errorf("missing tool_init data")
		}

		rawValue, exists := init["pass_trhough_value"]
		if !exists {
			return "", fmt.Errorf("missing required tool_init key: pass_trhough_value")
		}

		value, ok := rawValue.(string)
		if !ok {
			return "", fmt.Errorf("tool_init key pass_trhough_value must be a string")
		}

		if value == "" {
			return "", fmt.Errorf("tool_init key pass_trhough_value must not be empty")
		}

		return value, nil
	},
}
