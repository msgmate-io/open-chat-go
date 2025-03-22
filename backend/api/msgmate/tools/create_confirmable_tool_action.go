package tools

import (
	"encoding/json"
	"fmt"
)

type ConfirmableActionSuggestionInput struct {
	SuggestedInputs string `json:"suggested_inputs"`
}

var CreateConfirmableActionSuggestionToolDef = ToolDefinition{
	Name:                 "create_confirmable_action_suggestion",
	Description:          "Create a confirmable action suggestion payload that can be reviewed and approved before execution.",
	RequiresInit:         true,
	RequiresConfirmation: true,
	InputType:            ConfirmableActionSuggestionInput{},
	RequiredParams:       []string{"suggested_inputs"},
	Parameters: map[string]interface{}{
		"suggested_inputs": map[string]interface{}{"type": "string", "description": "Json encoded action input suggestion (editable)"},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		toolInput := input.(ConfirmableActionSuggestionInput)

		if toolInput.SuggestedInputs == "" {
			return "", fmt.Errorf("missing suggested_inputs")
		}

		if !json.Valid([]byte(toolInput.SuggestedInputs)) {
			return "", fmt.Errorf("suggested_inputs must be valid JSON")
		}

		return toolInput.SuggestedInputs, nil

	},
}
