package msgmate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---- Weather tool ----------

type WeatherTool struct {
	BaseTool
}

type WeatherToolInput struct {
	Location string `json:"location"`
	Unit     string `json:"unit"`
}

func (t *WeatherTool) RunTool(input interface{}) (string, error) {
	// time.Sleep(2 * time.Second)
	var weatherToolInput WeatherToolInput = input.(WeatherToolInput)
	return "The temperature in " + weatherToolInput.Location + " is " + strconv.Itoa(rand.Intn(100)) + " " + weatherToolInput.Unit, nil
}

func (t *WeatherTool) ParseArguments(input string) (interface{}, error) {
	var weatherInput WeatherToolInput
	err := json.Unmarshal([]byte(input), &weatherInput)
	if err != nil {
		return nil, err
	}
	return weatherInput, nil
}

func NewWeatherTool() Tool {
	weatherTool := &WeatherTool{}
	weatherTool.BaseTool = BaseTool{
		RequiresInit:    false,
		ToolName:        "get_weather",
		ToolType:        "function",
		ToolInput:       WeatherToolInput{},
		ToolInit:        interface{}(nil),
		ToolDescription: "Return the temperature of the specified region specified by the user",
		RequiredParams:  []string{"location", "unit"},
		Parameters: map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "The location to get weather for",
			},
			"unit": map[string]interface{}{
				"type":        "string",
				"description": "The unit of temperature (C or F)",
			},
		},
	}
	return weatherTool
}

// ----- Current Time tool ----------

type CurrentTimeTool struct {
	BaseTool
}

func (t *CurrentTimeTool) RunTool(input interface{}) (string, error) {
	return time.Now().Format(time.RFC3339), nil
}

func NewCurrentTimeTool() Tool {
	currentTimeTool := &CurrentTimeTool{}
	currentTimeTool.BaseTool = BaseTool{
		RequiresInit:    false,
		ToolName:        "get_current_time",
		ToolType:        "function",
		ToolInput:       interface{}(nil),
		ToolInit:        interface{}(nil),
		ToolDescription: "Return the current time",
		RequiredParams:  []string{},
		Parameters:      map[string]interface{}{},
	}
	return currentTimeTool
}

// ----- Random Number tool ----------

type RandomNumberTool struct {
	BaseTool
}

type RandomNumberToolInput struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func (t *RandomNumberTool) RunTool(input interface{}) (string, error) {
	var randomInput RandomNumberToolInput = input.(RandomNumberToolInput)

	// Ensure min is less than max
	if randomInput.Min >= randomInput.Max {
		return "", fmt.Errorf("min must be less than max")
	}

	// Generate random number in range [min, max]
	randomNum := rand.Intn(randomInput.Max-randomInput.Min+1) + randomInput.Min

	return strconv.Itoa(randomNum), nil
}

func (t *RandomNumberTool) ParseArguments(input string) (interface{}, error) {
	var randomInput RandomNumberToolInput
	err := json.Unmarshal([]byte(input), &randomInput)
	if err != nil {
		return nil, err
	}
	return randomInput, nil
}

func NewRandomNumberTool() Tool {
	randomNumberTool := &RandomNumberTool{}
	randomNumberTool.BaseTool = BaseTool{
		RequiresInit:    false,
		ToolName:        "get_random_number",
		ToolType:        "function",
		ToolInput:       RandomNumberToolInput{},
		ToolInit:        interface{}(nil),
		ToolDescription: "Generate a random number within a specified range",
		RequiredParams:  []string{"min", "max"},
		Parameters: map[string]interface{}{
			"min": map[string]interface{}{
				"type":        "integer",
				"description": "The minimum value (inclusive)",
			},
			"max": map[string]interface{}{
				"type":        "integer",
				"description": "The maximum value (inclusive)",
			},
		},
	}
	return randomNumberTool
}

// ----- Little World Chat Reply Tool ----

/**
1. A Little World User send a message to a support chat
2. A minmal integration in Little Worlds api calls `POST /api/chats/create/?chat_type=little_world_chat_support`
Body: {"tool_init_data": {"chat_uuid": "chat_uuid", "session_id": "session_id", "xcsrf_token": "xcsrf_token"}}
Response: {"chat_uuid": "chat_uuid"}
3. The agent is invoked by sending a message to the chat `POST /api/messages/<str:chat_uuid>/send/`
**/

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
