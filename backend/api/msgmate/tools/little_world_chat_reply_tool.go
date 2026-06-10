package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type LittleWorldChatReplyToolInput struct {
	Message string `json:"message"`
}

var LittleWorldChatReplyToolDef = ToolDefinition{
	Name:           "little_world__chat_reply",
	Description:    "Reply to a user's message in a Little World support chat.",
	AdminOnly:      true,
	RequiresInit:   true,
	InputType:      LittleWorldChatReplyToolInput{},
	RequiredParams: []string{"message"},
	Parameters: map[string]interface{}{
		"message": map[string]interface{}{"type": "string", "description": "The message to reply with"},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		toolInput := input.(LittleWorldChatReplyToolInput)
		sessionID, csrfToken, apiHost, chatUUID, err := extractChatInitData(initData)
		if err != nil {
			return "", err
		}
		body := new(bytes.Buffer)
		if err := json.NewEncoder(body).Encode(map[string]interface{}{"text": toolInput.Message}); err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}
		fullURL := fmt.Sprintf("%s/api/messages/%s/send/", apiHost, chatUUID)
		if _, err := makeAPIRequest("POST", fullURL, body, sessionID, csrfToken); err != nil {
			return fmt.Sprintf("error sending message: %s", err), err
		}
		return "Message sent successfully", nil
	},
}
