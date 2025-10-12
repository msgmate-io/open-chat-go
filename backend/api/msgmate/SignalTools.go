package msgmate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"
)

// --- Signal Send Message Tool ---

type SignalSendMessageToolInput struct {
	Message string `json:"message"`
}

type SignalSendMessageToolInit struct {
	RecipientPhone      string `json:"recipient_phone"`
	SenderPhone         string `json:"sender_phone"`
	ApiHost             string `json:"api_host"`
	BackendHost         string `json:"backend_host"`
	ChatUUID            string `json:"chat_uuid"`
	SignalUserSessionId string `json:"signal_user_session_id"`
	SignalUserUUID      string `json:"signal_user_uuid"`
}

var SignalSendMessageToolDef = ToolDefinition{
	Name:           "signal_send_message",
	Description:    "Send a message to the user",
	RequiresInit:   true,
	InputType:      SignalSendMessageToolInput{},
	RequiredParams: []string{"message"},
	Parameters: map[string]interface{}{
		"message": map[string]interface{}{
			"type":        "string",
			"description": "The message text to send to the user",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		log.Printf("[SignalTool] Starting signal_send_message tool execution")
		var messageInput = input.(SignalSendMessageToolInput)

		// Log all initialization data for debugging
		log.Printf("[SignalTool] Init data keys: %v", getMapKeys(initData))

		recipientPhone, ok := initData["recipient_phone"].(string)
		if !ok || recipientPhone == "" {
			log.Printf("[SignalTool] Error: missing or invalid recipient_phone")
			return "", fmt.Errorf("missing or invalid recipient_phone in initialization data")
		}
		log.Printf("[SignalTool] Recipient phone: %s", recipientPhone)

		senderPhone, ok := initData["sender_phone"].(string)
		if !ok || senderPhone == "" {
			log.Printf("[SignalTool] Error: missing or invalid sender_phone")
			return "", fmt.Errorf("missing or invalid sender_phone in initialization data")
		}
		log.Printf("[SignalTool] Sender phone: %s", senderPhone)

		apiHost, ok := initData["api_host"].(string)
		if !ok || apiHost == "" {
			log.Printf("[SignalTool] Error: missing or invalid api_host")
			return "", fmt.Errorf("missing or invalid api_host in initialization data")
		}
		log.Printf("[SignalTool] API host: %s", apiHost)

		backendHost, ok := initData["backend_host"].(string)
		if !ok || backendHost == "" {
			log.Printf("[SignalTool] Error: missing or invalid backend_host")
			return "", fmt.Errorf("missing or invalid backend_host in initialization data")
		}
		log.Printf("[SignalTool] Backend host: %s", backendHost)

		chatUUID, ok := initData["chat_uuid"].(string)
		if !ok || chatUUID == "" {
			log.Printf("[SignalTool] Error: missing or invalid chat_uuid")
			return "", fmt.Errorf("missing or invalid chat_uuid in initialization data")
		}
		log.Printf("[SignalTool] Chat UUID: %s", chatUUID)

		signalUserSessionId, ok := initData["signal_user_session_id"].(string)
		if !ok || signalUserSessionId == "" {
			log.Printf("[SignalTool] Error: missing or invalid signal_user_session_id")
			return "", fmt.Errorf("missing or invalid signal_user_session_id in initialization data")
		}
		log.Printf("[SignalTool] Signal user session ID: %s", signalUserSessionId)

		// PART 1: Send message via Signal REST API
		log.Printf("[SignalTool] Preparing to send message via Signal REST API")

		// Prepare the request to the Signal REST API
		url := fmt.Sprintf("%s/v2/send", apiHost)
		log.Printf("[SignalTool] Signal API URL: %s", url)

		// Prefix the message with [Tim's AI]:
		messageText := fmt.Sprintf("[👾]: %s", messageInput.Message)

		// Use senderPhone as the number and recipientPhone as the recipient
		requestBody := map[string]interface{}{
			"message":    messageText,
			"number":     senderPhone,
			"recipients": []string{recipientPhone},
		}

		bodyBytes, err := json.Marshal(requestBody)
		if err != nil {
			log.Printf("[SignalTool] Error marshaling Signal request body: %v", err)
			return "", fmt.Errorf("failed to marshal request body: %w", err)
		}
		log.Printf("[SignalTool] Signal request body: %s", string(bodyBytes))

		// Send the request to Signal API
		log.Printf("[SignalTool] Sending request to Signal API")
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(bodyBytes))
		if err != nil {
			log.Printf("[SignalTool] Error sending Signal message: %v", err)
			return "", fmt.Errorf("failed to send Signal message: %w", err)
		}
		defer resp.Body.Close()

		// Read and log the response body
		signalRespBody, _ := ioutil.ReadAll(resp.Body)
		log.Printf("[SignalTool] Signal API response status: %d, body: %s", resp.StatusCode, string(signalRespBody))

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			log.Printf("[SignalTool] Signal API returned non-success status: %d", resp.StatusCode)
			return "", fmt.Errorf("Signal API returned non-success status: %d, body: %s", resp.StatusCode, string(signalRespBody))
		}
		log.Printf("[SignalTool] Successfully sent message via Signal API")

		// PART 2: Send the message to the chat system
		log.Printf("[SignalTool] Preparing to send message to chat system")

		chatMessageURL := fmt.Sprintf("%s/api/v1/chats/%s/messages/send", backendHost, chatUUID)
		log.Printf("[SignalTool] Chat message URL: %s", chatMessageURL)

		// Prepare the chat message request body
		chatMessageBody := map[string]interface{}{
			"text": messageText,
		}

		chatBodyBytes, err := json.Marshal(chatMessageBody)
		if err != nil {
			log.Printf("[SignalTool] Error marshaling chat message body: %v", err)
			return "", fmt.Errorf("failed to marshal chat message body: %w", err)
		}
		log.Printf("[SignalTool] Chat message body: %s", string(chatBodyBytes))

		// Create a new request to send the message to the chat
		chatReq, err := http.NewRequest("POST", chatMessageURL, bytes.NewBuffer(chatBodyBytes))
		if err != nil {
			log.Printf("[SignalTool] Error creating chat message request: %v", err)
			return "", fmt.Errorf("failed to create chat message request: %w", err)
		}

		// Set the required headers
		chatReq.Header.Set("Content-Type", "application/json")
		chatReq.Header.Set("Origin", backendHost)
		chatReq.Header.Set("Cookie", fmt.Sprintf("session_id=%s", signalUserSessionId))

		log.Printf("[SignalTool] Chat request headers: Content-Type=%s, Origin=%s, Cookie=%s",
			chatReq.Header.Get("Content-Type"),
			chatReq.Header.Get("Origin"),
			chatReq.Header.Get("Cookie"))

		// Send the chat message request
		log.Printf("[SignalTool] Sending request to chat API")
		client := &http.Client{}
		chatResp, err := client.Do(chatReq)
		if err != nil {
			log.Printf("[SignalTool] Error sending chat message: %v", err)
			return "", fmt.Errorf("failed to send chat message: %w", err)
		}
		defer chatResp.Body.Close()

		// Read and log the response body
		chatRespBody, _ := ioutil.ReadAll(chatResp.Body)
		log.Printf("[SignalTool] Chat API response status: %d, body: %s", chatResp.StatusCode, string(chatRespBody))

		if chatResp.StatusCode != http.StatusOK {
			log.Printf("[SignalTool] Chat API returned non-OK status: %d", chatResp.StatusCode)
			return "", fmt.Errorf("chat API returned non-OK status: %d, body: %s", chatResp.StatusCode, string(chatRespBody))
		}
		log.Printf("[SignalTool] Successfully sent message to chat system")

		log.Printf("[SignalTool] Tool execution completed successfully")
		return fmt.Sprintf("Message '%s' sent successfully to %s", messageText, recipientPhone), nil
	},
}

func NewSignalSendMessageTool() Tool {
	return NewToolFromDefinition(SignalSendMessageToolDef)
}

// --- Signal Read Past Messages Tool ---

type SignalReadPastMessagesToolInput struct {
	Page  *int `json:"page,omitempty"`  // Optional page number (default: 1)
	Limit *int `json:"limit,omitempty"` // Optional page size (default: 20)
}

var SignalReadPastMessagesToolDef = ToolDefinition{
	Name:           "signal_read_past_messages",
	Description:    "Read past messages with the user. Use page and limit parameters to control pagination. Always use this tool if your not sure what the user is talking about or if it seems the user is referencing an earlier message.",
	RequiresInit:   true,
	InputType:      SignalReadPastMessagesToolInput{},
	RequiredParams: []string{},
	Parameters: map[string]interface{}{
		"page": map[string]interface{}{
			"type":        "integer",
			"description": "Page number to retrieve (default: 1)",
		},
		"limit": map[string]interface{}{
			"type":        "integer",
			"description": "Number of messages per page (default: 20)",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		log.Printf("[SignalTool] Starting signal_read_past_messages tool execution")
		var messageInput = input.(SignalReadPastMessagesToolInput)

		// Log all initialization data for debugging
		log.Printf("[SignalTool] Init data keys: %v", getMapKeys(initData))

		// Verify the required initialization parameters
		recipientPhone, ok := initData["recipient_phone"].(string)
		if !ok || recipientPhone == "" {
			log.Printf("[SignalTool] Error: missing or invalid recipient_phone")
			return "", fmt.Errorf("missing or invalid recipient_phone in initialization data")
		}
		log.Printf("[SignalTool] Recipient phone: %s", recipientPhone)

		senderPhone, ok := initData["sender_phone"].(string)
		if !ok || senderPhone == "" {
			log.Printf("[SignalTool] Error: missing or invalid sender_phone")
			return "", fmt.Errorf("missing or invalid sender_phone in initialization data")
		}
		log.Printf("[SignalTool] Sender phone: %s", senderPhone)

		backendHost, ok := initData["backend_host"].(string)
		if !ok || backendHost == "" {
			log.Printf("[SignalTool] Error: missing or invalid backend_host")
			return "", fmt.Errorf("missing or invalid backend_host in initialization data")
		}
		log.Printf("[SignalTool] Backend host: %s", backendHost)

		chatUUID, ok := initData["chat_uuid"].(string)
		if !ok || chatUUID == "" {
			log.Printf("[SignalTool] Error: missing or invalid chat_uuid")
			return "", fmt.Errorf("missing or invalid chat_uuid in initialization data")
		}
		log.Printf("[SignalTool] Chat UUID: %s", chatUUID)

		signalUserSessionId, ok := initData["signal_user_session_id"].(string)
		if !ok || signalUserSessionId == "" {
			log.Printf("[SignalTool] Error: missing or invalid signal_user_session_id")
			return "", fmt.Errorf("missing or invalid signal_user_session_id in initialization data")
		}
		log.Printf("[SignalTool] Signal user session ID: %s", signalUserSessionId)

		signalUserUUID, ok := initData["signal_user_uuid"].(string)
		if !ok || signalUserUUID == "" {
			log.Printf("[SignalTool] Error: missing or invalid signal_user_uuid")
			return "", fmt.Errorf("missing or invalid signal_user_uuid in initialization data")
		}
		log.Printf("[SignalTool] Signal user UUID: %s", signalUserUUID)

		// Set default values for pagination
		page := 1
		limit := 20

		// Use input parameters if provided
		if messageInput.Page != nil && *messageInput.Page > 0 {
			page = *messageInput.Page
		}
		if messageInput.Limit != nil && *messageInput.Limit > 0 {
			limit = *messageInput.Limit
		}

		// Prepare the request to fetch past messages
		log.Printf("[SignalTool] Preparing to fetch past messages (page: %d, limit: %d)", page, limit)

		// Build the URL for the messages list API
		messagesURL := fmt.Sprintf("%s/api/v1/chats/%s/messages/list?page=%d&limit=%d", backendHost, chatUUID, page, limit)
		log.Printf("[SignalTool] Messages URL: %s", messagesURL)

		// Create a new request to fetch messages
		req, err := http.NewRequest("GET", messagesURL, nil)
		if err != nil {
			log.Printf("[SignalTool] Error creating request: %v", err)
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		// Set the required headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", backendHost)
		req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", signalUserSessionId))

		log.Printf("[SignalTool] Request headers: Content-Type=%s, Origin=%s, Cookie=session_id=%s",
			req.Header.Get("Content-Type"),
			req.Header.Get("Origin"),
			req.Header.Get("Cookie"))

		// Send the request
		log.Printf("[SignalTool] Sending request to fetch messages")
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[SignalTool] Error fetching messages: %v", err)
			return "", fmt.Errorf("failed to fetch messages: %w", err)
		}
		defer resp.Body.Close()

		// Read and log the response body
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[SignalTool] Error reading response body: %v", err)
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		log.Printf("[SignalTool] API response status: %d", resp.StatusCode)
		// Only log a preview of the response body to avoid flooding logs
		if len(respBody) > 200 {
			log.Printf("[SignalTool] Response body preview: %s...", respBody[:200])
		} else {
			log.Printf("[SignalTool] Response body: %s", respBody)
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("[SignalTool] API returned non-OK status: %d", resp.StatusCode)
			return "", fmt.Errorf("API returned non-OK status: %d, body: %s", resp.StatusCode, string(respBody))
		}

		// Parse the response (assuming it's a JSON response)
		var paginatedMessages struct {
			Rows []struct {
				UUID       string                  `json:"uuid"`
				SendAt     string                  `json:"send_at"`
				SenderUUID string                  `json:"sender_uuid"`
				Text       string                  `json:"text"`
				Reasoning  *[]string               `json:"reasoning"`
				ToolCalls  *[]interface{}          `json:"tool_calls"`
				MetaData   *map[string]interface{} `json:"meta_data"`
			} `json:"rows"`
			Total      int `json:"total"`
			Page       int `json:"page"`
			Limit      int `json:"limit"`
			TotalPages int `json:"total_pages"`
		}

		if err := json.Unmarshal(respBody, &paginatedMessages); err != nil {
			log.Printf("[SignalTool] Error parsing response: %v", err)
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		// Format the messages for readability
		var formattedMessages strings.Builder

		// Include complete pagination information
		formattedMessages.WriteString(fmt.Sprintf("Retrieved %d messages (of %d total) from Signal chat\n",
			len(paginatedMessages.Rows), paginatedMessages.Total))
		formattedMessages.WriteString(fmt.Sprintf("Page %d of %d (Limit: %d messages per page)\n\n",
			paginatedMessages.Page, paginatedMessages.TotalPages, paginatedMessages.Limit))

		// Display messages from oldest to newest
		for i := len(paginatedMessages.Rows) - 1; i >= 0; i-- {
			msg := paginatedMessages.Rows[i]
			// Format the timestamp
			sentTime := msg.SendAt
			// Try to parse the timestamp if possible
			if t, err := time.Parse(time.RFC3339, msg.SendAt); err == nil {
				sentTime = t.Format("Jan 2, 2006 15:04:05")
			}

			// Determine sender based on sender_uuid comparison with signal_user_uuid
			var sender string
			if msg.SenderUUID == signalUserUUID {
				sender = "AI"
			} else {
				sender = "User"
			}

			formattedMessages.WriteString(fmt.Sprintf("[%s] %s: %s\n", sentTime, sender, msg.Text))
		}

		log.Printf("[SignalTool] Tool execution completed successfully")
		return formattedMessages.String(), nil
	},
}

func NewSignalReadPastMessagesTool() Tool {
	return NewToolFromDefinition(SignalReadPastMessagesToolDef)
}

// --- Signal Get Whitelist Tool ---

type SignalGetWhitelistToolInput struct {
	// Empty input as we don't need additional parameters to get the whitelist
}

type SignalWhitelistToolInit struct {
	RecipientPhone      string `json:"recipient_phone"`
	SenderPhone         string `json:"sender_phone"`
	ApiHost             string `json:"api_host"`
	BackendHost         string `json:"backend_host"`
	ChatUUID            string `json:"chat_uuid"`
	SignalUserSessionId string `json:"signal_user_session_id"`
	AdminUserSessionId  string `json:"admin_user_session_id"`
	IntegrationAlias    string `json:"integration_alias"`
}

var SignalGetWhitelistToolDef = ToolDefinition{
	Name:           "signal_get_whitelist",
	Description:    "Get the current whitelist for the Signal integration",
	RequiresInit:   true,
	InputType:      SignalGetWhitelistToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		log.Printf("[SignalTool] Starting signal_get_whitelist tool execution")

		// Log all initialization data for debugging
		log.Printf("[SignalTool] Init data keys: %v", getMapKeys(initData))

		// Verify the required initialization parameters
		backendHost, ok := initData["backend_host"].(string)
		if !ok || backendHost == "" {
			log.Printf("[SignalTool] Error: missing or invalid backend_host")
			return "", fmt.Errorf("missing or invalid backend_host in initialization data")
		}
		log.Printf("[SignalTool] Backend host: %s", backendHost)

		adminUserSessionId, ok := initData["admin_user_session_id"].(string)
		if !ok || adminUserSessionId == "" {
			log.Printf("[SignalTool] Error: missing or invalid admin_user_session_id")
			return "", fmt.Errorf("missing or invalid admin_user_session_id in initialization data")
		}
		log.Printf("[SignalTool] Admin user session ID: %s", adminUserSessionId)

		integrationAlias, ok := initData["integration_alias"].(string)
		if !ok || integrationAlias == "" {
			log.Printf("[SignalTool] Error: missing or invalid integration_alias")
			return "", fmt.Errorf("missing or invalid integration_alias in initialization data")
		}
		log.Printf("[SignalTool] Integration alias: %s", integrationAlias)

		// Prepare the request to fetch the whitelist
		log.Printf("[SignalTool] Preparing to fetch Signal whitelist")

		// Build the URL for the whitelist API
		whitelistURL := fmt.Sprintf("%s/api/v1/integrations/signal/whitelist?alias=%s", backendHost, integrationAlias)
		log.Printf("[SignalTool] Whitelist URL: %s", whitelistURL)

		// Create a new request to fetch the whitelist
		req, err := http.NewRequest("GET", whitelistURL, nil)
		if err != nil {
			log.Printf("[SignalTool] Error creating request: %v", err)
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		// Set the required headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", backendHost)
		req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", adminUserSessionId))

		log.Printf("[SignalTool] Request headers: Content-Type=%s, Origin=%s, Cookie=session_id=%s",
			req.Header.Get("Content-Type"),
			req.Header.Get("Origin"),
			req.Header.Get("Cookie"))

		// Send the request
		log.Printf("[SignalTool] Sending request to fetch whitelist")
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[SignalTool] Error fetching whitelist: %v", err)
			return "", fmt.Errorf("failed to fetch whitelist: %w", err)
		}
		defer resp.Body.Close()

		// Read and log the response body
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[SignalTool] Error reading response body: %v", err)
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		log.Printf("[SignalTool] API response status: %d", resp.StatusCode)
		log.Printf("[SignalTool] Response body: %s", respBody)

		if resp.StatusCode != http.StatusOK {
			log.Printf("[SignalTool] API returned non-OK status: %d", resp.StatusCode)
			return "", fmt.Errorf("API returned non-OK status: %d, body: %s", resp.StatusCode, string(respBody))
		}

		// Parse the response
		var response struct {
			Alias     string   `json:"alias"`
			Whitelist []string `json:"whitelist"`
		}

		if err := json.Unmarshal(respBody, &response); err != nil {
			log.Printf("[SignalTool] Error parsing response: %v", err)
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		// Format the whitelist for output
		var output string
		if len(response.Whitelist) == 0 {
			output = fmt.Sprintf("The whitelist for Signal integration '%s' is empty.", response.Alias)
		} else {
			output = fmt.Sprintf("Whitelist for Signal integration '%s':\n", response.Alias)
			for i, number := range response.Whitelist {
				output += fmt.Sprintf("%d. %s\n", i+1, number)
			}
		}

		log.Printf("[SignalTool] Tool execution completed successfully")
		return output, nil
	},
}

func NewSignalGetWhitelistTool() Tool {
	return NewToolFromDefinition(SignalGetWhitelistToolDef)
}

// --- Signal Add to Whitelist Tool ---

type SignalAddToWhitelistToolInput struct {
	PhoneNumber string `json:"phone_number"`
}

var SignalAddToWhitelistToolDef = ToolDefinition{
	Name:           "signal_add_to_whitelist",
	Description:    "Add a phone number to the Signal integration whitelist",
	RequiresInit:   true,
	InputType:      SignalAddToWhitelistToolInput{},
	RequiredParams: []string{"phone_number"},
	Parameters: map[string]interface{}{
		"phone_number": map[string]interface{}{
			"type":        "string",
			"description": "The phone number to add to the whitelist",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		log.Printf("[SignalTool] Starting signal_add_to_whitelist tool execution")
		var addInput = input.(SignalAddToWhitelistToolInput)

		// Log all initialization data for debugging
		log.Printf("[SignalTool] Init data keys: %v", getMapKeys(initData))

		// Verify the required initialization parameters
		backendHost, ok := initData["backend_host"].(string)
		if !ok || backendHost == "" {
			log.Printf("[SignalTool] Error: missing or invalid backend_host")
			return "", fmt.Errorf("missing or invalid backend_host in initialization data")
		}
		log.Printf("[SignalTool] Backend host: %s", backendHost)

		adminUserSessionId, ok := initData["admin_user_session_id"].(string)
		if !ok || adminUserSessionId == "" {
			log.Printf("[SignalTool] Error: missing or invalid admin_user_session_id")
			return "", fmt.Errorf("missing or invalid admin_user_session_id in initialization data")
		}
		log.Printf("[SignalTool] Admin user session ID: %s", adminUserSessionId)

		integrationAlias, ok := initData["integration_alias"].(string)
		if !ok || integrationAlias == "" {
			log.Printf("[SignalTool] Error: missing or invalid integration_alias")
			return "", fmt.Errorf("missing or invalid integration_alias in initialization data")
		}
		log.Printf("[SignalTool] Integration alias: %s", integrationAlias)

		// Verify the phone number
		if addInput.PhoneNumber == "" {
			log.Printf("[SignalTool] Error: missing phone_number")
			return "", fmt.Errorf("missing phone_number in input data")
		}
		log.Printf("[SignalTool] Phone number to add: %s", addInput.PhoneNumber)

		// Prepare the request to add the phone number to the whitelist
		log.Printf("[SignalTool] Preparing to add phone number to Signal whitelist")

		// Build the URL for the whitelist add API
		whitelistAddURL := fmt.Sprintf("%s/api/v1/integrations/signal/whitelist/add", backendHost)
		log.Printf("[SignalTool] Whitelist add URL: %s", whitelistAddURL)

		// Prepare the request body
		requestBody := map[string]interface{}{
			"alias":        integrationAlias,
			"phone_number": addInput.PhoneNumber,
		}

		bodyBytes, err := json.Marshal(requestBody)
		if err != nil {
			log.Printf("[SignalTool] Error marshaling request body: %v", err)
			return "", fmt.Errorf("failed to marshal request body: %w", err)
		}
		log.Printf("[SignalTool] Request body: %s", string(bodyBytes))

		// Create a new request to add to the whitelist
		req, err := http.NewRequest("POST", whitelistAddURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			log.Printf("[SignalTool] Error creating request: %v", err)
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		// Set the required headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", backendHost)
		req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", adminUserSessionId))

		log.Printf("[SignalTool] Request headers: Content-Type=%s, Origin=%s, Cookie=session_id=%s",
			req.Header.Get("Content-Type"),
			req.Header.Get("Origin"),
			req.Header.Get("Cookie"))

		// Send the request
		log.Printf("[SignalTool] Sending request to add to whitelist")
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[SignalTool] Error adding to whitelist: %v", err)
			return "", fmt.Errorf("failed to add to whitelist: %w", err)
		}
		defer resp.Body.Close()

		// Read and log the response body
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[SignalTool] Error reading response body: %v", err)
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		log.Printf("[SignalTool] API response status: %d", resp.StatusCode)
		log.Printf("[SignalTool] Response body: %s", respBody)

		if resp.StatusCode != http.StatusOK {
			log.Printf("[SignalTool] API returned non-OK status: %d", resp.StatusCode)
			return "", fmt.Errorf("API returned non-OK status: %d, body: %s", resp.StatusCode, string(respBody))
		}

		// Parse the response
		var response struct {
			Message   string   `json:"message"`
			Whitelist []string `json:"whitelist"`
		}

		if err := json.Unmarshal(respBody, &response); err != nil {
			log.Printf("[SignalTool] Error parsing response: %v", err)
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		// Check if the number was actually added or was already in whitelist
		if strings.Contains(response.Message, "already in whitelist") {
			log.Printf("[SignalTool] Phone number already in whitelist: %s", addInput.PhoneNumber)
		}

		// Validate that the number appears in the returned whitelist
		numberFound := false
		for _, number := range response.Whitelist {
			if number == addInput.PhoneNumber {
				numberFound = true
				break
			}
		}

		if !numberFound {
			log.Printf("[SignalTool] Warning: Phone number %s not found in returned whitelist", addInput.PhoneNumber)
			return response.Message + " (Warning: Number not confirmed in whitelist)", nil
		}

		log.Printf("[SignalTool] Tool execution completed successfully")
		return response.Message, nil
	},
}

func NewSignalAddToWhitelistTool() Tool {
	return NewToolFromDefinition(SignalAddToWhitelistToolDef)
}

// --- Signal Remove from Whitelist Tool ---

type SignalRemoveFromWhitelistToolInput struct {
	PhoneNumber string `json:"phone_number"`
}

var SignalRemoveFromWhitelistToolDef = ToolDefinition{
	Name:           "signal_remove_from_whitelist",
	Description:    "Remove a phone number from the Signal integration whitelist",
	RequiresInit:   true,
	InputType:      SignalRemoveFromWhitelistToolInput{},
	RequiredParams: []string{"phone_number"},
	Parameters: map[string]interface{}{
		"phone_number": map[string]interface{}{
			"type":        "string",
			"description": "The phone number to remove from the whitelist",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		log.Printf("[SignalTool] Starting signal_remove_from_whitelist tool execution")
		var removeInput = input.(SignalRemoveFromWhitelistToolInput)

		// Log all initialization data for debugging
		log.Printf("[SignalTool] Init data keys: %v", getMapKeys(initData))

		// Verify the required initialization parameters
		backendHost, ok := initData["backend_host"].(string)
		if !ok || backendHost == "" {
			log.Printf("[SignalTool] Error: missing or invalid backend_host")
			return "", fmt.Errorf("missing or invalid backend_host in initialization data")
		}
		log.Printf("[SignalTool] Backend host: %s", backendHost)

		adminUserSessionId, ok := initData["admin_user_session_id"].(string)
		if !ok || adminUserSessionId == "" {
			log.Printf("[SignalTool] Error: missing or invalid admin_user_session_id")
			return "", fmt.Errorf("missing or invalid admin_user_session_id in initialization data")
		}
		log.Printf("[SignalTool] Admin user session ID: %s", adminUserSessionId)

		integrationAlias, ok := initData["integration_alias"].(string)
		if !ok || integrationAlias == "" {
			log.Printf("[SignalTool] Error: missing or invalid integration_alias")
			return "", fmt.Errorf("missing or invalid integration_alias in initialization data")
		}
		log.Printf("[SignalTool] Integration alias: %s", integrationAlias)

		// Verify the phone number
		if removeInput.PhoneNumber == "" {
			log.Printf("[SignalTool] Error: missing phone_number")
			return "", fmt.Errorf("missing phone_number in input data")
		}
		log.Printf("[SignalTool] Phone number to remove: %s", removeInput.PhoneNumber)

		// Prepare the request to remove the phone number from the whitelist
		log.Printf("[SignalTool] Preparing to remove phone number from Signal whitelist")

		// Build the URL for the whitelist remove API
		whitelistRemoveURL := fmt.Sprintf("%s/api/v1/integrations/signal/whitelist/remove", backendHost)
		log.Printf("[SignalTool] Whitelist remove URL: %s", whitelistRemoveURL)

		// Prepare the request body
		requestBody := map[string]interface{}{
			"alias":        integrationAlias,
			"phone_number": removeInput.PhoneNumber,
		}

		bodyBytes, err := json.Marshal(requestBody)
		if err != nil {
			log.Printf("[SignalTool] Error marshaling request body: %v", err)
			return "", fmt.Errorf("failed to marshal request body: %w", err)
		}
		log.Printf("[SignalTool] Request body: %s", string(bodyBytes))

		// Create a new request to remove from the whitelist
		req, err := http.NewRequest("DELETE", whitelistRemoveURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			log.Printf("[SignalTool] Error creating request: %v", err)
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		// Set the required headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", backendHost)
		req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", adminUserSessionId))

		log.Printf("[SignalTool] Request headers: Content-Type=%s, Origin=%s, Cookie=session_id=%s",
			req.Header.Get("Content-Type"),
			req.Header.Get("Origin"),
			req.Header.Get("Cookie"))

		// Send the request
		log.Printf("[SignalTool] Sending request to remove from whitelist")
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[SignalTool] Error removing from whitelist: %v", err)
			return "", fmt.Errorf("failed to remove from whitelist: %w", err)
		}
		defer resp.Body.Close()

		// Read and log the response body
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[SignalTool] Error reading response body: %v", err)
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		log.Printf("[SignalTool] API response status: %d", resp.StatusCode)
		log.Printf("[SignalTool] Response body: %s", respBody)

		if resp.StatusCode != http.StatusOK {
			log.Printf("[SignalTool] API returned non-OK status: %d", resp.StatusCode)
			return "", fmt.Errorf("API returned non-OK status: %d, body: %s", resp.StatusCode, string(respBody))
		}

		// Parse the response
		var response struct {
			Message   string   `json:"message"`
			Whitelist []string `json:"whitelist"`
		}

		if err := json.Unmarshal(respBody, &response); err != nil {
			log.Printf("[SignalTool] Error parsing response: %v", err)
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		log.Printf("[SignalTool] Tool execution completed successfully")
		return response.Message, nil
	},
}

func NewSignalRemoveFromWhitelistTool() Tool {
	return NewToolFromDefinition(SignalRemoveFromWhitelistToolDef)
}

// --- Signal Show Typing Indicator Tool ---

type SignalShowTypingIndicatorToolInput struct {
	// Empty input as we don't need additional parameters to show typing indicator
}

var SignalShowTypingIndicatorToolDef = ToolDefinition{
	Name:           "signal_show_typing_indicator",
	Description:    "Show a typing indicator to the user",
	RequiresInit:   true,
	InputType:      SignalShowTypingIndicatorToolInput{},
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		log.Printf("[SignalTool] Starting signal_show_typing_indicator tool execution")

		// Log all initialization data for debugging
		log.Printf("[SignalTool] Init data keys: %v", getMapKeys(initData))

		// Verify the required initialization parameters
		recipientPhone, ok := initData["recipient_phone"].(string)
		if !ok || recipientPhone == "" {
			log.Printf("[SignalTool] Error: missing or invalid recipient_phone")
			return "", fmt.Errorf("missing or invalid recipient_phone in initialization data")
		}
		log.Printf("[SignalTool] Recipient phone: %s", recipientPhone)

		senderPhone, ok := initData["sender_phone"].(string)
		if !ok || senderPhone == "" {
			log.Printf("[SignalTool] Error: missing or invalid sender_phone")
			return "", fmt.Errorf("missing or invalid sender_phone in initialization data")
		}
		log.Printf("[SignalTool] Sender phone: %s", senderPhone)

		apiHost, ok := initData["api_host"].(string)
		if !ok || apiHost == "" {
			log.Printf("[SignalTool] Error: missing or invalid api_host")
			return "", fmt.Errorf("missing or invalid api_host in initialization data")
		}
		log.Printf("[SignalTool] API host: %s", apiHost)

		// Prepare the request to the Signal REST API
		log.Printf("[SignalTool] Preparing to send typing indicator via Signal REST API")

		// Prepare the request to the Signal REST API
		url := fmt.Sprintf("%s/v1/typing-indicator/%s", apiHost, senderPhone)
		log.Printf("[SignalTool] Signal API URL: %s", url)

		// Prepare request body
		requestBody := map[string]interface{}{
			"recipient": recipientPhone,
		}

		bodyBytes, err := json.Marshal(requestBody)
		if err != nil {
			log.Printf("[SignalTool] Error marshaling typing indicator request body: %v", err)
			return "", fmt.Errorf("failed to marshal request body: %w", err)
		}
		log.Printf("[SignalTool] Signal typing indicator request body: %s", string(bodyBytes))

		// Create a new request
		req, err := http.NewRequest("PUT", url, bytes.NewBuffer(bodyBytes))
		if err != nil {
			log.Printf("[SignalTool] Error creating typing indicator request: %v", err)
			return "", fmt.Errorf("failed to create typing indicator request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		// Send the request to Signal API
		log.Printf("[SignalTool] Sending typing indicator request to Signal API")
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[SignalTool] Error sending typing indicator: %v", err)
			return "", fmt.Errorf("failed to send typing indicator: %w", err)
		}
		defer resp.Body.Close()

		// Read and log the response body
		signalRespBody, _ := ioutil.ReadAll(resp.Body)
		log.Printf("[SignalTool] Signal API response status: %d, body: %s", resp.StatusCode, string(signalRespBody))

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			log.Printf("[SignalTool] Signal API returned non-success status: %d", resp.StatusCode)
			return "", fmt.Errorf("Signal API returned non-success status: %d, body: %s", resp.StatusCode, string(signalRespBody))
		}
		log.Printf("[SignalTool] Successfully sent typing indicator via Signal API")

		log.Printf("[SignalTool] Tool execution completed successfully")
		return fmt.Sprintf("Typing indicator shown to %s", recipientPhone), nil
	},
}

func NewSignalShowTypingIndicatorTool() Tool {
	return NewToolFromDefinition(SignalShowTypingIndicatorToolDef)
}
