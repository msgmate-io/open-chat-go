package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type LittleWorldGenerateMessageReplySuggestionToolInput struct {
	Message string `json:"message"`
}

var LittleWorldGenerateMessageReplySuggestionToolDef = ToolDefinition{
	Name:         "little_world__generate_message_reply_suggestion",
	Description:  "Persist the final support reply suggestion by calling this tool; this tool must be called for the draft to be saved.",
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
			"action_id": map[string]interface{}{
				"type":        "string",
				"description": "Identifier of the open support task action to update.",
				"minLength":   1,
			},
			"mock_run": map[string]interface{}{
				"type":        "boolean",
				"description": "Optional testing flag. If true, skip the API request and return a mocked success result.",
			},
		},
		"required":             []string{"session_id", "csrf_token", "api_host", "task_pk", "action_id"},
		"additionalProperties": false,
		"description":          "Initialization data required to authenticate and target one specific Little World support task action.",
	},
	InputType: LittleWorldGenerateMessageReplySuggestionToolInput{},
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Final draft support reply text that will be persisted when this tool is called.",
				"minLength":   1,
			},
		},
		"required":             []string{"message"},
		"additionalProperties": false,
		"description":          "Payload for persisting the support action message suggestion; calling this tool performs the save.",
	},
	RequiredParams: []string{"message"},
	Parameters: map[string]interface{}{
		"message": map[string]interface{}{"type": "string", "description": "The updated draft message suggestion to persist"},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		toolInput := input.(LittleWorldGenerateMessageReplySuggestionToolInput)
		sessionID, csrfToken, apiHost, taskPK, actionID, err := extractSupportTaskActionInitData(initData)
		if err != nil {
			return "", err
		}
		if mockRun, _ := initData["mock_run"].(bool); mockRun {
			return "Message reply suggestion updated successfully (mock run)", nil
		}

		body := new(bytes.Buffer)
		if err := json.NewEncoder(body).Encode(map[string]interface{}{
			"action_id": actionID,
			"parameters": map[string]interface{}{
				"message": toolInput.Message,
			},
		}); err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}

		fullURL := buildAPIURL(apiHost, fmt.Sprintf("/api/support_task/%s/action/", taskPK))
		if _, err := makeAPIRequest("PATCH", fullURL, body, sessionID, csrfToken); err != nil {
			return fmt.Sprintf("error updating message reply suggestion: %s", err), err
		}
		return "Message reply suggestion updated successfully", nil
	},
}
