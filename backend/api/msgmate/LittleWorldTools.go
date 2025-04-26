package msgmate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type LittleWorldChatReplyTool struct {
	BaseTool
}

type LittleWorldChatReplyToolInput struct {
	Message string `json:"message"`
}

type LittleWorldChatReplyToolInit struct {
	ChatUUID   string `json:"chat_uuid"`
	SessionID  string `json:"session_id"`
	XCSRFToken string `json:"xcsrf_token"`
	APIHost    string `json:"api_host"`
}

func (t *LittleWorldChatReplyTool) RunTool(input interface{}) (string, error) {
	var littleWorldChatReplyToolInput LittleWorldChatReplyToolInput = input.(LittleWorldChatReplyToolInput)

	// Convert the ToolInit from map[string]interface{} to LittleWorldChatReplyToolInit
	initData, ok := t.ToolInit.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid tool initialization data type")
	}

	// Extract the fields from the map
	chatUUID, _ := initData["chat_uuid"].(string)
	sessionID, _ := initData["session_id"].(string)
	xcsrfToken, _ := initData["csrf_token"].(string)
	apiHost, _ := initData["api_host"].(string)

	// Create the properly typed init struct
	toolInit := LittleWorldChatReplyToolInit{
		ChatUUID:   chatUUID,
		SessionID:  sessionID,
		XCSRFToken: xcsrfToken,
		APIHost:    apiHost,
	}

	// Make api request
	// set the request body
	body := new(bytes.Buffer)
	err := json.NewEncoder(body).Encode(map[string]interface{}{
		"text": littleWorldChatReplyToolInput.Message,
	})
	if err != nil {
		return fmt.Sprintf("error encoding request body %s", err), err
	}

	fullURL := fmt.Sprintf("%s/api/messages/%s/send/", toolInit.APIHost, toolInit.ChatUUID)
	fmt.Println("Full URL: ", fullURL)
	req, err := http.NewRequest("POST", fullURL, body)
	if err != nil {
		return fmt.Sprintf("error creating request %s", err), err
	}

	req.Header.Set("Content-Type", "application/json")
	// Set both session_id and sessionid cookies to ensure compatibility
	req.Header.Set("Cookie", fmt.Sprintf("sessionid=%s; csrftoken=%s", toolInit.SessionID, toolInit.XCSRFToken))
	req.Header.Set("X-CSRFToken", toolInit.XCSRFToken)
	fmt.Println("Using X-CSRFToken: ", toolInit.XCSRFToken)
	fmt.Println("Using ChatUUID: ", toolInit.ChatUUID)
	fmt.Println("Using SessionID: ", toolInit.SessionID)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("error sending message %s", err), err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read the response body to get the error details
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Sprintf("error: received non-200 response code: %d, and failed to read response body: %s",
				resp.StatusCode, readErr), fmt.Errorf("non-200 status code: %d", resp.StatusCode)
		}

		return fmt.Sprintf("error: received non-200 response code: %d, response body: %s",
			resp.StatusCode, string(responseBody)), fmt.Errorf("non-200 status code: %d, response: %s", resp.StatusCode, string(responseBody))
	}

	return "send chat message", nil
}

func (t *LittleWorldChatReplyTool) ParseArguments(input string) (interface{}, error) {
	var replyInput LittleWorldChatReplyToolInput
	err := json.Unmarshal([]byte(input), &replyInput)
	if err != nil {
		return nil, err
	}
	return replyInput, nil
}

func NewLittleWorldChatReplyTool() Tool {
	littleWorldChatReplyTool := &LittleWorldChatReplyTool{}
	littleWorldChatReplyTool.BaseTool = BaseTool{
		RequiresInit:    true,
		ToolName:        "little_world__chat_reply",
		ToolType:        "function",
		ToolDescription: "Reply to a user's message in a Little World support chat",
		RequiredParams:  []string{"message"},
		Parameters: map[string]interface{}{
			"message": map[string]interface{}{
				"type":        "string",
				"description": "The message to reply to",
			},
		},
	}
	return littleWorldChatReplyTool
}

// --- Little World Get Past Messages With User Tool ---

type LittleWorldGetPastMessagesWithUserTool struct {
	BaseTool
}

type LittleWorldGetPastMessagesWithUserToolInput struct{}

type LittleWorldGetPastMessagesWithUserToolInit struct {
	ChatUUID   string `json:"chat_uuid"`
	SessionID  string `json:"session_id"`
	XCSRFToken string `json:"xcsrf_token"`
	APIHost    string `json:"api_host"`
}

func (t *LittleWorldGetPastMessagesWithUserTool) RunTool(input interface{}) (string, error) {
	// Convert the ToolInit from map[string]interface{} to LittleWorldGetPastMessagesWithUserToolInit
	initData, ok := t.ToolInit.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid tool initialization data type")
	}

	// Extract the fields from the map
	chatUUID, _ := initData["chat_uuid"].(string)
	sessionID, _ := initData["session_id"].(string)
	xcsrfToken, _ := initData["csrf_token"].(string)
	apiHost, _ := initData["api_host"].(string)

	// Create the properly typed init struct
	toolInit := LittleWorldGetPastMessagesWithUserToolInit{
		ChatUUID:   chatUUID,
		SessionID:  sessionID,
		XCSRFToken: xcsrfToken,
		APIHost:    apiHost,
	}

	// Make API request to get past messages
	fullURL := fmt.Sprintf("%s/api/messages/%s/", toolInit.APIHost, toolInit.ChatUUID)
	fmt.Println("Full URL: ", fullURL)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return fmt.Sprintf("error creating request %s", err), err
	}

	req.Header.Set("Content-Type", "application/json")
	// Set both session_id and sessionid cookies to ensure compatibility
	req.Header.Set("Cookie", fmt.Sprintf("sessionid=%s; csrftoken=%s", toolInit.SessionID, toolInit.XCSRFToken))
	req.Header.Set("X-CSRFToken", toolInit.XCSRFToken)
	fmt.Println("Using X-CSRFToken: ", toolInit.XCSRFToken)
	fmt.Println("Using ChatUUID: ", toolInit.ChatUUID)
	fmt.Println("Using SessionID: ", toolInit.SessionID)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("error fetching messages %s", err), err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read the response body to get the error details
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Sprintf("error: received non-200 response code: %d, and failed to read response body: %s",
				resp.StatusCode, readErr), fmt.Errorf("non-200 status code: %d", resp.StatusCode)
		}

		return fmt.Sprintf("error: received non-200 response code: %d, response body: %s",
			resp.StatusCode, string(responseBody)), fmt.Errorf("non-200 status code: %d, response: %s", resp.StatusCode, string(responseBody))
	}

	// Read and parse the response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("error reading response body: %s", err), err
	}

	// Return the raw JSON response as the tool result
	return string(responseBody), nil
}

func (t *LittleWorldGetPastMessagesWithUserTool) ParseArguments(input string) (interface{}, error) {
	// This tool doesn't require any input parameters, so we just return an empty struct
	return LittleWorldGetPastMessagesWithUserToolInput{}, nil
}

func NewLittleWorldGetPastMessagesWithUserTool() Tool {
	littleWorldGetPastMessagesWithUserTool := &LittleWorldGetPastMessagesWithUserTool{}
	littleWorldGetPastMessagesWithUserTool.BaseTool = BaseTool{
		RequiresInit:    true,
		ToolName:        "little_world__get_past_messages",
		ToolType:        "function",
		ToolInput:       LittleWorldGetPastMessagesWithUserToolInput{},
		ToolInit:        interface{}(nil),
		ToolDescription: "Retrieve past messages from a Little World support chat",
		RequiredParams:  []string{},
		Parameters:      map[string]interface{}{},
	}
	return littleWorldGetPastMessagesWithUserTool
}

// --- Little World Set User Searching State Tool ---

type LittleWorldSetUserSearchingStateTool struct {
	BaseTool
}

type LittleWorldSetUserSearchingStateToolInput struct {
	Searching bool `json:"searching"`
}

type LittleWorldSetUserSearchingStateToolInit struct {
	UserId     string `json:"user_id"`
	SessionID  string `json:"session_id"`
	XCSRFToken string `json:"xcsrf_token"`
	APIHost    string `json:"api_host"`
}

func (t *LittleWorldSetUserSearchingStateTool) RunTool(input interface{}) (string, error) {
	var toolInput LittleWorldSetUserSearchingStateToolInput = input.(LittleWorldSetUserSearchingStateToolInput)

	// Convert the ToolInit from map[string]interface{} to LittleWorldSetUserSearchingStateToolInit
	initData, ok := t.ToolInit.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid tool initialization data type")
	}

	// Extract the fields from the map
	var userId string
	userIdRaw := initData["user_id"]
	switch v := userIdRaw.(type) {
	case string:
		userId = v
	case float64:
		userId = fmt.Sprintf("%.0f", v)
	case int:
		userId = fmt.Sprintf("%d", v)
	case int64:
		userId = fmt.Sprintf("%d", v)
	default:
		if userIdRaw != nil {
			userId = fmt.Sprintf("%v", userIdRaw)
		}
	}
	sessionID, _ := initData["session_id"].(string)
	xcsrfToken, _ := initData["csrf_token"].(string)
	apiHost, _ := initData["api_host"].(string)

	// Create the properly typed init struct
	toolInit := LittleWorldSetUserSearchingStateToolInit{
		UserId:     userId,
		SessionID:  sessionID,
		XCSRFToken: xcsrfToken,
		APIHost:    apiHost,
	}

	// Make API request to change user searching state
	body := new(bytes.Buffer)
	searchingState := "idle"
	if toolInput.Searching {
		searchingState = "searching"
	}

	err := json.NewEncoder(body).Encode(map[string]interface{}{
		"searching_state": searchingState,
	})
	if err != nil {
		return fmt.Sprintf("error encoding request body %s", err), err
	}

	fullURL := fmt.Sprintf("%s/api/matching/users/%s/change_searching_state/", toolInit.APIHost, toolInit.UserId)
	fmt.Println("Full URL: ", fullURL)

	req, err := http.NewRequest("POST", fullURL, body)
	if err != nil {
		return fmt.Sprintf("error creating request %s", err), err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf("sessionid=%s; csrftoken=%s", toolInit.SessionID, toolInit.XCSRFToken))
	req.Header.Set("X-CSRFToken", toolInit.XCSRFToken)
	fmt.Println("Using X-CSRFToken: ", toolInit.XCSRFToken)
	fmt.Println("Using UserId: ", toolInit.UserId)
	fmt.Println("Using SessionID: ", toolInit.SessionID)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("error changing user searching state %s", err), err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read the response body to get the error details
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Sprintf("error: received non-200 response code: %d, and failed to read response body: %s",
				resp.StatusCode, readErr), fmt.Errorf("non-200 status code: %d", resp.StatusCode)
		}

		return fmt.Sprintf("error: received non-200 response code: %d, response body: %s",
			resp.StatusCode, string(responseBody)), fmt.Errorf("non-200 status code: %d, response: %s", resp.StatusCode, string(responseBody))
	}

	// Read and parse the response
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("error reading response body: %s", err), err
	}

	// Return success message with the new searching state
	if toolInput.Searching {
		return "Successfully set user searching state to active", nil
	} else {
		return "Successfully set user searching state to inactive", nil
	}
}

func (t *LittleWorldSetUserSearchingStateTool) ParseArguments(input string) (interface{}, error) {
	var searchingInput LittleWorldSetUserSearchingStateToolInput
	err := json.Unmarshal([]byte(input), &searchingInput)
	if err != nil {
		return nil, err
	}
	return searchingInput, nil
}

func NewLittleWorldSetUserSearchingStateTool() Tool {
	littleWorldSetUserSearchingStateTool := &LittleWorldSetUserSearchingStateTool{}
	littleWorldSetUserSearchingStateTool.BaseTool = BaseTool{
		RequiresInit:    true,
		ToolName:        "little_world__set_user_searching_state",
		ToolType:        "function",
		ToolInput:       LittleWorldSetUserSearchingStateToolInput{},
		ToolInit:        interface{}(nil),
		ToolDescription: "Change a user's searching state in Little World",
		RequiredParams:  []string{"searching"},
		Parameters: map[string]interface{}{
			"searching": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the user should be in searching state (true) or not (false)",
			},
		},
	}
	return littleWorldSetUserSearchingStateTool
}

// --- Little World Get User State Tool ---

type LittleWorldGetUserStateTool struct {
	BaseTool
}

type LittleWorldGetUserStateToolInput struct{}

type LittleWorldGetUserStateToolInit struct {
	UserId     string `json:"user_id"`
	SessionID  string `json:"session_id"`
	XCSRFToken string `json:"xcsrf_token"`
	APIHost    string `json:"api_host"`
}

func (t *LittleWorldGetUserStateTool) RunTool(input interface{}) (string, error) {
	// Convert the ToolInit from map[string]interface{} to LittleWorldGetUserStateToolInit
	initData, ok := t.ToolInit.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid tool initialization data type")
	}

	fmt.Println("InitData: ", initData)

	// Extract the fields from the map
	var userId string

	// Handle user_id which could be a number (float64 in JSON) or string
	userIdRaw := initData["user_id"]
	switch v := userIdRaw.(type) {
	case string:
		userId = v
	case float64:
		userId = fmt.Sprintf("%.0f", v)
	case int:
		userId = fmt.Sprintf("%d", v)
	case int64:
		userId = fmt.Sprintf("%d", v)
	default:
		if userIdRaw != nil {
			userId = fmt.Sprintf("%v", userIdRaw)
		}
	}

	sessionID, _ := initData["session_id"].(string)
	xcsrfToken, _ := initData["csrf_token"].(string)
	apiHost, _ := initData["api_host"].(string)

	// Create the properly typed init struct
	toolInit := LittleWorldGetUserStateToolInit{
		UserId:     userId,
		SessionID:  sessionID,
		XCSRFToken: xcsrfToken,
		APIHost:    apiHost,
	}

	// Make API request to get user state
	fullURL := fmt.Sprintf("%s/api/matching/users/%s/", toolInit.APIHost, toolInit.UserId)
	fmt.Println("Full URL: ", fullURL)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return fmt.Sprintf("error creating request %s", err), err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf("sessionid=%s; csrftoken=%s", toolInit.SessionID, toolInit.XCSRFToken))
	req.Header.Set("X-CSRFToken", toolInit.XCSRFToken)
	fmt.Println("Using X-CSRFToken: ", toolInit.XCSRFToken)
	fmt.Println("Using UserId: ", toolInit.UserId)
	fmt.Println("Using SessionID: ", toolInit.SessionID)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("error fetching user state %s", err), err
	}
	defer resp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("error reading response body: %s", err), err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("error: received non-200 response code: %d, response body: %s",
			resp.StatusCode, string(responseBody)), fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	// Check if response starts with '<' which indicates HTML instead of JSON
	responseStr := string(responseBody)
	if strings.TrimSpace(responseStr)[0] == '<' {
		return fmt.Sprintf("error: received HTML response instead of JSON: %s", responseStr),
			fmt.Errorf("received HTML response instead of JSON")
	}

	// Parse the JSON response to extract just the state information
	var responseData map[string]interface{}
	err = json.Unmarshal(responseBody, &responseData)
	if err != nil {
		return fmt.Sprintf("error parsing response JSON: %s, raw response: %s", err, responseStr), err
	}

	// Extract the state information
	state, ok := responseData["state"]
	if !ok {
		return fmt.Sprintf("state information not found in response: %s", responseStr),
			fmt.Errorf("state information not found in response")
	}

	// Return just the state information
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Sprintf("error marshaling state data: %s", err), err
	}

	return string(stateJSON), nil
}

func (t *LittleWorldGetUserStateTool) ParseArguments(input string) (interface{}, error) {
	// This tool doesn't require any input parameters, so we just return an empty struct
	return LittleWorldGetUserStateToolInput{}, nil
}

func NewLittleWorldGetUserStateTool() Tool {
	littleWorldGetUserStateTool := &LittleWorldGetUserStateTool{}
	littleWorldGetUserStateTool.BaseTool = BaseTool{
		RequiresInit:    true,
		ToolName:        "little_world__get_user_state",
		ToolType:        "function",
		ToolInput:       LittleWorldGetUserStateToolInput{},
		ToolInit:        interface{}(nil),
		ToolDescription: "Get the current state of a user in Little World. This also contains information on e.g.: if the user is searching for a match",
		RequiredParams:  []string{},
		Parameters:      map[string]interface{}{},
	}
	return littleWorldGetUserStateTool
}

// --- Little World Retrieve Match Overview Tool ---

type LittleWorldRetrieveMatchOverviewTool struct {
	BaseTool
}

type LittleWorldRetrieveMatchOverviewToolInput struct{}

type LittleWorldRetrieveMatchOverviewToolInit struct {
	UserId     string `json:"user_id"`
	SessionID  string `json:"session_id"`
	XCSRFToken string `json:"xcsrf_token"`
	APIHost    string `json:"api_host"`
}

func (t *LittleWorldRetrieveMatchOverviewTool) RunTool(input interface{}) (string, error) {
	// Convert the ToolInit from map[string]interface{} to LittleWorldRetrieveMatchOverviewToolInit
	initData, ok := t.ToolInit.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid tool initialization data type")
	}

	fmt.Println("InitData: ", initData)

	// Extract the fields from the map
	var userId string

	// Handle user_id which could be a number (float64 in JSON) or string
	userIdRaw := initData["user_id"]
	switch v := userIdRaw.(type) {
	case string:
		userId = v
	case float64:
		userId = fmt.Sprintf("%.0f", v)
	case int:
		userId = fmt.Sprintf("%d", v)
	case int64:
		userId = fmt.Sprintf("%d", v)
	default:
		if userIdRaw != nil {
			userId = fmt.Sprintf("%v", userIdRaw)
		}
	}

	sessionID, _ := initData["session_id"].(string)
	xcsrfToken, _ := initData["csrf_token"].(string)
	apiHost, _ := initData["api_host"].(string)

	// Create the properly typed init struct
	toolInit := LittleWorldRetrieveMatchOverviewToolInit{
		UserId:     userId,
		SessionID:  sessionID,
		XCSRFToken: xcsrfToken,
		APIHost:    apiHost,
	}

	// Make API request to get user matches
	fullURL := fmt.Sprintf("%s/api/matching/users/%s/", toolInit.APIHost, toolInit.UserId)
	fmt.Println("Full URL: ", fullURL)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return fmt.Sprintf("error creating request %s", err), err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf("sessionid=%s; csrftoken=%s", toolInit.SessionID, toolInit.XCSRFToken))
	req.Header.Set("X-CSRFToken", toolInit.XCSRFToken)
	fmt.Println("Using X-CSRFToken: ", toolInit.XCSRFToken)
	fmt.Println("Using UserId: ", toolInit.UserId)
	fmt.Println("Using SessionID: ", toolInit.SessionID)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("error fetching user matches %s", err), err
	}
	defer resp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("error reading response body: %s", err), err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("error: received non-200 response code: %d, response body: %s",
			resp.StatusCode, string(responseBody)), fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	// Check if response starts with '<' which indicates HTML instead of JSON
	responseStr := string(responseBody)
	if strings.TrimSpace(responseStr)[0] == '<' {
		return fmt.Sprintf("error: received HTML response instead of JSON: %s", responseStr),
			fmt.Errorf("received HTML response instead of JSON")
	}

	// Parse the JSON response to extract just the matches information
	var responseData map[string]interface{}
	err = json.Unmarshal(responseBody, &responseData)
	if err != nil {
		return fmt.Sprintf("error parsing response JSON: %s, raw response: %s", err, responseStr), err
	}

	// Extract the matches information
	matches, ok := responseData["matches"]
	if !ok {
		return fmt.Sprintf("matches information not found in response: %s", responseStr),
			fmt.Errorf("matches information not found in response")
	}

	// Return just the matches information
	matchesJSON, err := json.Marshal(matches)
	if err != nil {
		return fmt.Sprintf("error marshaling matches data: %s", err), err
	}

	return string(matchesJSON), nil
}

func (t *LittleWorldRetrieveMatchOverviewTool) ParseArguments(input string) (interface{}, error) {
	// This tool doesn't require any input parameters, so we just return an empty struct
	return LittleWorldRetrieveMatchOverviewToolInput{}, nil
}

func NewLittleWorldRetrieveMatchOverviewTool() Tool {
	littleWorldRetrieveMatchOverviewTool := &LittleWorldRetrieveMatchOverviewTool{}
	littleWorldRetrieveMatchOverviewTool.BaseTool = BaseTool{
		RequiresInit:    true,
		ToolName:        "little_world__retrieve_match_overview",
		ToolType:        "function",
		ToolInput:       LittleWorldRetrieveMatchOverviewToolInput{},
		ToolInit:        interface{}(nil),
		ToolDescription: "Retrieve an overview of the user's matches in Little World",
		RequiredParams:  []string{},
		Parameters:      map[string]interface{}{},
	}
	return littleWorldRetrieveMatchOverviewTool
}
