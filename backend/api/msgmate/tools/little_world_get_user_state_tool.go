package tools

import "fmt"

type LittleWorldGetUserStateToolInput struct{}

var LittleWorldGetUserStateToolDef = ToolDefinition{
	Name:           "little_world__get_user_state",
	Description:    "Get the current state of a user in Little World.",
	RequiresInit:   true,
	InputType:      LittleWorldGetUserStateToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(_ interface{}, initData map[string]interface{}) (string, error) {
		sessionID, csrfToken, apiHost, userID, err := extractUserInitData(initData)
		if err != nil {
			return "", err
		}
		fullURL := fmt.Sprintf("%s/api/matching/users/%s/", apiHost, userID)
		responseBody, err := makeAPIRequest("GET", fullURL, nil, sessionID, csrfToken)
		if err != nil {
			return fmt.Sprintf("error fetching user state: %s", err), err
		}
		stateJSON, err := extractJSONField(responseBody, "state")
		if err != nil {
			return "", err
		}
		return string(stateJSON), nil
	},
}
