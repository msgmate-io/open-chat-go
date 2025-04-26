package msgmate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ExtractUserID handles the various ways a user ID might be represented
func ExtractUserID(userIdRaw interface{}) string {
	if userIdRaw == nil {
		return ""
	}

	switch v := userIdRaw.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%v", userIdRaw)
	}
}

// MakeAPIRequest is a helper for common API request patterns
func MakeAPIRequest(method, url string, body io.Reader, sessionID, xcsrfToken string) ([]byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf("sessionid=%s; csrftoken=%s", sessionID, xcsrfToken))
	req.Header.Set("X-CSRFToken", xcsrfToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("non-200 status code: %d, response: %s", resp.StatusCode, string(responseBody))
	}

	return responseBody, nil
}

// ExtractInitData extracts common fields from tool initialization data
func ExtractInitData(initData map[string]interface{}) (string, string, string, string, error) {
	sessionID, _ := initData["session_id"].(string)
	xcsrfToken, _ := initData["csrf_token"].(string)
	apiHost, _ := initData["api_host"].(string)

	if sessionID == "" || xcsrfToken == "" || apiHost == "" {
		return "", "", "", "", fmt.Errorf("missing required initialization data")
	}

	return sessionID, xcsrfToken, apiHost, "", nil
}

// ExtractChatInitData extracts chat-specific initialization data
func ExtractChatInitData(initData map[string]interface{}) (string, string, string, string, error) {
	sessionID, xcsrfToken, apiHost, _, err := ExtractInitData(initData)
	if err != nil {
		return "", "", "", "", err
	}

	chatUUID, _ := initData["chat_uuid"].(string)
	if chatUUID == "" {
		return "", "", "", "", fmt.Errorf("missing chat_uuid in initialization data")
	}

	return sessionID, xcsrfToken, apiHost, chatUUID, nil
}

// ExtractUserInitData extracts user-specific initialization data
func ExtractUserInitData(initData map[string]interface{}) (string, string, string, string, error) {
	sessionID, xcsrfToken, apiHost, _, err := ExtractInitData(initData)
	if err != nil {
		return "", "", "", "", err
	}

	userId := ExtractUserID(initData["user_id"])
	if userId == "" {
		return "", "", "", "", fmt.Errorf("missing user_id in initialization data")
	}

	return sessionID, xcsrfToken, apiHost, userId, nil
}

// ValidateJSONResponse checks if the response is valid JSON
func ValidateJSONResponse(responseBody []byte) error {
	responseStr := string(responseBody)
	if strings.TrimSpace(responseStr)[0] == '<' {
		return fmt.Errorf("received HTML response instead of JSON: %s", responseStr)
	}
	return nil
}

// ExtractJSONField extracts a specific field from a JSON response
func ExtractJSONField(responseBody []byte, fieldName string) ([]byte, error) {
	err := ValidateJSONResponse(responseBody)
	if err != nil {
		return nil, err
	}

	var responseData map[string]interface{}
	err = json.Unmarshal(responseBody, &responseData)
	if err != nil {
		return nil, fmt.Errorf("error parsing response JSON: %s", err)
	}

	field, ok := responseData[fieldName]
	if !ok {
		return nil, fmt.Errorf("%s not found in response", fieldName)
	}

	fieldJSON, err := json.Marshal(field)
	if err != nil {
		return nil, fmt.Errorf("error marshaling %s data: %s", fieldName, err)
	}

	return fieldJSON, nil
}
