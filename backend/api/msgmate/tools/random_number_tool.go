package tools

import (
	"fmt"
	"math/rand"
	"strconv"
)

type RandomNumberToolInput struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

var RandomNumberToolDef = ToolDefinition{
	Name:           "get_random_number",
	Description:    "Generate a random number within a specified range.",
	RequiresInit:   false,
	InputType:      RandomNumberToolInput{},
	RequiredParams: []string{"min", "max"},
	Parameters: map[string]interface{}{
		"min": map[string]interface{}{"type": "integer", "description": "The minimum value (inclusive)"},
		"max": map[string]interface{}{"type": "integer", "description": "The maximum value (inclusive)"},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		toolInput := input.(RandomNumberToolInput)
		if toolInput.Min >= toolInput.Max {
			return "", fmt.Errorf("min must be less than max")
		}
		return strconv.Itoa(rand.Intn(toolInput.Max-toolInput.Min+1) + toolInput.Min), nil
	},
}
