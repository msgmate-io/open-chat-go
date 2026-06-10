package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type LittleWorldSetUserSearchingStateToolInput struct {
	Searching bool `json:"searching"`
}

var LittleWorldSetUserSearchingStateToolDef = ToolDefinition{
	Name:           "little_world__set_user_searching_state",
	Description:    "Change a user's searching state in Little World.",
	AdminOnly:      true,
	RequiresInit:   true,
	InputType:      LittleWorldSetUserSearchingStateToolInput{},
	RequiredParams: []string{"searching"},
	Parameters: map[string]interface{}{
		"searching": map[string]interface{}{"type": "boolean", "description": "Whether the user should be searching"},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		toolInput := input.(LittleWorldSetUserSearchingStateToolInput)
		sessionID, csrfToken, apiHost, userID, err := extractUserInitData(initData)
		if err != nil {
			return "", err
		}
		searchingState := "idle"
		if toolInput.Searching {
			searchingState = "searching"
		}
		body := new(bytes.Buffer)
		if err := json.NewEncoder(body).Encode(map[string]interface{}{"searching_state": searchingState}); err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}
		fullURL := fmt.Sprintf("%s/api/matching/users/%s/change_searching_state/", apiHost, userID)
		if _, err := makeAPIRequest("POST", fullURL, body, sessionID, csrfToken); err != nil {
			return fmt.Sprintf("error changing user searching state: %s", err), err
		}
		if toolInput.Searching {
			return "Successfully set user searching state to active", nil
		}
		return "Successfully set user searching state to inactive", nil
	},
}
