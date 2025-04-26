package msgmate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// --- Little World Chat Reply Tool ---

type LittleWorldChatReplyToolInput struct {
	Message string `json:"message"`
}

var LittleWorldChatReplyToolDef = ToolDefinition{
	Name:           "little_world__chat_reply",
	Description:    "Reply to a user's message in a Little World support chat",
	RequiresInit:   true,
	InputType:      LittleWorldChatReplyToolInput{},
	RequiredParams: []string{"message"},
	Parameters: map[string]interface{}{
		"message": map[string]interface{}{
			"type":        "string",
			"description": "The message to reply to",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		var chatReplyInput = input.(LittleWorldChatReplyToolInput)

		// Extract initialization data
		sessionID, xcsrfToken, apiHost, chatUUID, err := ExtractChatInitData(initData)
		if err != nil {
			return "", err
		}

		// Prepare request body
		body := new(bytes.Buffer)
		err = json.NewEncoder(body).Encode(map[string]interface{}{
			"text": chatReplyInput.Message,
		})
		if err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}

		// Make API request
		fullURL := fmt.Sprintf("%s/api/messages/%s/send/", apiHost, chatUUID)
		fmt.Println("Full URL: ", fullURL)
		fmt.Println("Using X-CSRFToken: ", xcsrfToken)
		fmt.Println("Using ChatUUID: ", chatUUID)
		fmt.Println("Using SessionID: ", sessionID)

		_, err = MakeAPIRequest("POST", fullURL, body, sessionID, xcsrfToken)
		if err != nil {
			return fmt.Sprintf("error sending message: %s", err), err
		}

		return "Message sent successfully", nil
	},
}

func NewLittleWorldChatReplyTool() Tool {
	return NewToolFromDefinition(LittleWorldChatReplyToolDef)
}

// --- Little World Get Past Messages With User Tool ---

type LittleWorldGetPastMessagesWithUserToolInput struct{}

var LittleWorldGetPastMessagesWithUserToolDef = ToolDefinition{
	Name:           "little_world__get_past_messages",
	Description:    "Retrieve past messages from a Little World support chat",
	RequiresInit:   true,
	InputType:      LittleWorldGetPastMessagesWithUserToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		// Extract initialization data
		sessionID, xcsrfToken, apiHost, chatUUID, err := ExtractChatInitData(initData)
		if err != nil {
			return "", err
		}

		// Make API request to get past messages
		fullURL := fmt.Sprintf("%s/api/messages/%s/", apiHost, chatUUID)
		fmt.Println("Full URL: ", fullURL)
		fmt.Println("Using X-CSRFToken: ", xcsrfToken)
		fmt.Println("Using ChatUUID: ", chatUUID)
		fmt.Println("Using SessionID: ", sessionID)

		responseBody, err := MakeAPIRequest("GET", fullURL, nil, sessionID, xcsrfToken)
		if err != nil {
			return fmt.Sprintf("error fetching messages: %s", err), err
		}

		// Validate JSON response
		err = ValidateJSONResponse(responseBody)
		if err != nil {
			return "", err
		}

		// Return the raw JSON response
		return string(responseBody), nil
	},
}

func NewLittleWorldGetPastMessagesWithUserTool() Tool {
	return NewToolFromDefinition(LittleWorldGetPastMessagesWithUserToolDef)
}

// --- Little World Set User Searching State Tool ---

type LittleWorldSetUserSearchingStateToolInput struct {
	Searching bool `json:"searching"`
}

var LittleWorldSetUserSearchingStateToolDef = ToolDefinition{
	Name:           "little_world__set_user_searching_state",
	Description:    "Change a user's searching state in Little World",
	RequiresInit:   true,
	InputType:      LittleWorldSetUserSearchingStateToolInput{},
	RequiredParams: []string{"searching"},
	Parameters: map[string]interface{}{
		"searching": map[string]interface{}{
			"type":        "boolean",
			"description": "Whether the user should be in searching state (true) or not (false)",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		var toolInput = input.(LittleWorldSetUserSearchingStateToolInput)

		// Extract initialization data
		sessionID, xcsrfToken, apiHost, userId, err := ExtractUserInitData(initData)
		if err != nil {
			return "", err
		}

		// Prepare request body
		body := new(bytes.Buffer)
		searchingState := "idle"
		if toolInput.Searching {
			searchingState = "searching"
		}

		err = json.NewEncoder(body).Encode(map[string]interface{}{
			"searching_state": searchingState,
		})
		if err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}

		// Make API request
		fullURL := fmt.Sprintf("%s/api/matching/users/%s/change_searching_state/", apiHost, userId)
		fmt.Println("Full URL: ", fullURL)
		fmt.Println("Using X-CSRFToken: ", xcsrfToken)
		fmt.Println("Using UserId: ", userId)
		fmt.Println("Using SessionID: ", sessionID)

		_, err = MakeAPIRequest("POST", fullURL, body, sessionID, xcsrfToken)
		if err != nil {
			return fmt.Sprintf("error changing user searching state: %s", err), err
		}

		// Return success message
		if toolInput.Searching {
			return "Successfully set user searching state to active", nil
		} else {
			return "Successfully set user searching state to inactive", nil
		}
	},
}

func NewLittleWorldSetUserSearchingStateTool() Tool {
	return NewToolFromDefinition(LittleWorldSetUserSearchingStateToolDef)
}

// --- Little World Get User State Tool ---

type LittleWorldGetUserStateToolInput struct{}

var LittleWorldGetUserStateToolDef = ToolDefinition{
	Name:           "little_world__get_user_state",
	Description:    "Get the current state of a user in Little World. This also contains information on e.g.: if the user is searching for a match",
	RequiresInit:   true,
	InputType:      LittleWorldGetUserStateToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		// Extract initialization data
		sessionID, xcsrfToken, apiHost, userId, err := ExtractUserInitData(initData)
		if err != nil {
			return "", err
		}

		fmt.Println("InitData: ", initData)

		// Make API request
		fullURL := fmt.Sprintf("%s/api/matching/users/%s/", apiHost, userId)
		fmt.Println("Full URL: ", fullURL)
		fmt.Println("Using X-CSRFToken: ", xcsrfToken)
		fmt.Println("Using UserId: ", userId)
		fmt.Println("Using SessionID: ", sessionID)

		responseBody, err := MakeAPIRequest("GET", fullURL, nil, sessionID, xcsrfToken)
		if err != nil {
			return fmt.Sprintf("error fetching user state: %s", err), err
		}

		// Extract state field from response
		stateJSON, err := ExtractJSONField(responseBody, "state")
		if err != nil {
			return "", err
		}

		return string(stateJSON), nil
	},
}

func NewLittleWorldGetUserStateTool() Tool {
	return NewToolFromDefinition(LittleWorldGetUserStateToolDef)
}

// --- Little World Retrieve Match Overview Tool ---

type LittleWorldRetrieveMatchOverviewToolInput struct{}

var LittleWorldRetrieveMatchOverviewToolDef = ToolDefinition{
	Name:           "little_world__retrieve_match_overview",
	Description:    "Retrieve an overview of the user's matches in Little World",
	RequiresInit:   true,
	InputType:      LittleWorldRetrieveMatchOverviewToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		// Extract initialization data
		sessionID, xcsrfToken, apiHost, userId, err := ExtractUserInitData(initData)
		if err != nil {
			return "", err
		}

		fmt.Println("InitData: ", initData)

		// Make API request
		fullURL := fmt.Sprintf("%s/api/matching/users/%s/", apiHost, userId)
		fmt.Println("Full URL: ", fullURL)
		fmt.Println("Using X-CSRFToken: ", xcsrfToken)
		fmt.Println("Using UserId: ", userId)
		fmt.Println("Using SessionID: ", sessionID)

		responseBody, err := MakeAPIRequest("GET", fullURL, nil, sessionID, xcsrfToken)
		if err != nil {
			return fmt.Sprintf("error fetching user matches: %s", err), err
		}

		// Extract matches field from response
		matchesJSON, err := ExtractJSONField(responseBody, "matches")
		if err != nil {
			return "", err
		}

		return string(matchesJSON), nil
	},
}

func NewLittleWorldRetrieveMatchOverviewTool() Tool {
	return NewToolFromDefinition(LittleWorldRetrieveMatchOverviewToolDef)
}
