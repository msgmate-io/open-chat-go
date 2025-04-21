package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type LittleWorldGenerateMessageReplySuggestionToolInput struct {
	ActionID string `json:"action_id"`
	Message  string `json:"message"`
}

var LittleWorldGenerateMessageReplySuggestionToolDef = ToolDefinition{
	Name:         "little_world__generate_message_reply_suggestion",
	Description:  "Update an open Little World support task action draft message suggestion.",
	AdminOnly:    true,
	RequiresInit: true,
	InitSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_id": map[string]interface{}{
				"type":        "string",
				"description": "Session cookie value used to authenticate the Little World API request.",
				"minLength":   1,
			},
			"csrf_token": map[string]interface{}{
				"type":        "string",
				"description": "CSRF token paired with the session for Little World API calls.",
				"minLength":   1,
			},
			"api_host": map[string]interface{}{
				"type":        "string",
				"description": "Base URL of the Little World API host, for example https://app.littleworld.com.",
				"minLength":   1,
			},
			"task_pk": map[string]interface{}{
				"type":        "string",
				"description": "Primary key of the support task that owns the action draft.",
				"minLength":   1,
			},
		},
		"required":             []string{"session_id", "csrf_token", "api_host", "task_pk"},
		"additionalProperties": false,
		"description":          "Initialization data required to authenticate and target the Little World support task action API.",
	},
	InputType: LittleWorldGenerateMessageReplySuggestionToolInput{},
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action_id": map[string]interface{}{
				"type":        "string",
				"description": "Identifier of the open support task action to update.",
				"minLength":   1,
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Updated draft support reply text.",
				"minLength":   1,
			},
		},
		"required":             []string{"action_id", "message"},
		"additionalProperties": false,
		"description":          "Payload for updating one support action draft message suggestion.",
	},
	RequiredParams: []string{"action_id", "message"},
	Parameters: map[string]interface{}{
		"action_id": map[string]interface{}{"type": "string", "description": "Identifier of the open support task action to update"},
		"message":   map[string]interface{}{"type": "string", "description": "The updated draft message suggestion"},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		toolInput := input.(LittleWorldGenerateMessageReplySuggestionToolInput)
		sessionID, csrfToken, apiHost, taskPK, err := extractSupportTaskInitData(initData)
		if err != nil {
			return "", err
		}

		body := new(bytes.Buffer)
		if err := json.NewEncoder(body).Encode(map[string]interface{}{
			"action_id": toolInput.ActionID,
			"parameters": map[string]interface{}{
				"message": toolInput.Message,
			},
		}); err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}

		fullURL := fmt.Sprintf("%s/api/support_task/%s/action/", apiHost, taskPK)
		if _, err := makeAPIRequest("PATCH", fullURL, body, sessionID, csrfToken); err != nil {
			return fmt.Sprintf("error updating message reply suggestion: %s", err), err
		}
		return "Message reply suggestion updated successfully", nil
	},
}
