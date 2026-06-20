package tools

import (
	"encoding/json"
	"fmt"
)

type ConfirmableActionSuggestionInput struct {
	TargetToolName       string `json:"target_tool_name"`
	SuggestedInputs      string `json:"suggested_inputs"`
	ContinueAfterExecute bool   `json:"continue_after_execute,omitempty"`
	Title                string `json:"title,omitempty"`
	Description          string `json:"description,omitempty"`
	ConfirmLabel         string `json:"confirm_label,omitempty"`
	DangerLevel          string `json:"danger_level,omitempty"`
}

var CreateConfirmableActionSuggestionToolDef = ToolDefinition{
	Name:                 "create_confirmable_action_suggestion",
	Description:          "Create a confirmable action suggestion payload that can be reviewed and approved before execution.",
	RequiresInit:         true,
	RequiresConfirmation: false,
	InputType:            ConfirmableActionSuggestionInput{},
	RequiredParams:       []string{"target_tool_name", "suggested_inputs"},
	Parameters: map[string]interface{}{
		"target_tool_name":       map[string]interface{}{"type": "string", "description": "Name of the tool that should run after user confirmation"},
		"suggested_inputs":       map[string]interface{}{"type": "string", "description": "Json encoded action input suggestion (editable)"},
		"continue_after_execute": map[string]interface{}{"type": "boolean", "description": "Optional: if true, continue the interaction after this action executes"},
		"title":                  map[string]interface{}{"type": "string", "description": "Optional UI title for the confirmation widget"},
		"description":            map[string]interface{}{"type": "string", "description": "Optional explanation shown to the user before confirmation"},
		"confirm_label":          map[string]interface{}{"type": "string", "description": "Optional confirm button label"},
		"danger_level":           map[string]interface{}{"type": "string", "description": "Optional risk level hint: low|medium|high"},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		toolInput := input.(ConfirmableActionSuggestionInput)

		if toolInput.TargetToolName == "" {
			return "", fmt.Errorf("missing target_tool_name")
		}

		if toolInput.SuggestedInputs == "" {
			return "", fmt.Errorf("missing suggested_inputs")
		}

		if !json.Valid([]byte(toolInput.SuggestedInputs)) {
			return "", fmt.Errorf("suggested_inputs must be valid JSON")
		}

		var suggested map[string]interface{}
		if err := json.Unmarshal([]byte(toolInput.SuggestedInputs), &suggested); err != nil {
			return "", fmt.Errorf("suggested_inputs must decode to json object")
		}

		payload := map[string]interface{}{
			"type":                   "confirm-action",
			"status":                 "pending_confirmation",
			"requires_confirmation":  true,
			"target_tool_name":       toolInput.TargetToolName,
			"suggested_inputs":       suggested,
			"continue_after_execute": toolInput.ContinueAfterExecute,
		}
		if toolInput.Title != "" {
			payload["title"] = toolInput.Title
		}
		if toolInput.Description != "" {
			payload["description"] = toolInput.Description
		}
		if toolInput.ConfirmLabel != "" {
			payload["confirm_label"] = toolInput.ConfirmLabel
		}
		if toolInput.DangerLevel != "" {
			payload["danger_level"] = toolInput.DangerLevel
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}

		return string(encoded), nil

	},
}
