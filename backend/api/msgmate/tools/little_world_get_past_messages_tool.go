package tools

import "fmt"

type LittleWorldGetPastMessagesWithUserToolInput struct{}

var LittleWorldGetPastMessagesWithUserToolDef = ToolDefinition{
	Name:           "little_world__get_past_messages",
	Description:    "Retrieve past messages from a Little World support chat.",
	AdminOnly:      true,
	RequiresInit:   true,
	InputType:      LittleWorldGetPastMessagesWithUserToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(_ interface{}, initData map[string]interface{}) (string, error) {
		sessionID, csrfToken, apiHost, chatUUID, err := extractChatInitData(initData)
		if err != nil {
			return "", err
		}
		fullURL := fmt.Sprintf("%s/api/messages/%s/", apiHost, chatUUID)
		responseBody, err := makeAPIRequest("GET", fullURL, nil, sessionID, csrfToken)
		if err != nil {
			return fmt.Sprintf("error fetching messages: %s", err), err
		}
		if err := validateJSONResponse(responseBody); err != nil {
			return "", err
		}
		return string(responseBody), nil
	},
}
