package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const defaultAPITimeout = 30 * time.Second

func normalizeAPIHost(apiHost string) string {
	host := strings.TrimSpace(apiHost)
	return strings.TrimRight(host, "/")
}

func buildAPIURL(apiHost, path string) string {
	return fmt.Sprintf("%s%s", normalizeAPIHost(apiHost), path)
}

func newAPIHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultAPITimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

func applyAPIAuthHeaders(req *http.Request, sessionID, csrfToken string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf("sessionid=%s; csrftoken=%s", sessionID, csrfToken))
	req.Header.Set("X-CSRFToken", csrfToken)
}

func executeAPIRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptReq := req
		if attempt > 1 {
			if req.GetBody == nil {
				break
			}
			body, err := req.GetBody()
			if err != nil {
				lastErr = err
				break
			}
			attemptReq = req.Clone(req.Context())
			attemptReq.Body = body
		}

		resp, err := client.Do(attemptReq)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == maxAttempts {
			break
		}
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	return nil, lastErr
}

func extractUserID(userIDRaw interface{}) string {
	if userIDRaw == nil {
		return ""
	}
	switch v := userIDRaw.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%v", userIDRaw)
	}
}

func makeAPIRequest(method, url string, body io.Reader, sessionID, csrfToken string) ([]byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	applyAPIAuthHeaders(req, sessionID, csrfToken)

	resp, err := executeAPIRequest(newAPIHTTPClient(), req)
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

func makeRequestSimple(method, url string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func extractInitData(initData map[string]interface{}) (string, string, string, error) {
	sessionID, _ := initData["session_id"].(string)
	csrfToken, _ := initData["csrf_token"].(string)
	apiHostRaw, _ := initData["api_host"].(string)
	apiHost := normalizeAPIHost(apiHostRaw)
	if sessionID == "" || csrfToken == "" || apiHost == "" {
		return "", "", "", fmt.Errorf("missing required initialization data")
	}
	return sessionID, csrfToken, apiHost, nil
}

func extractChatInitData(initData map[string]interface{}) (string, string, string, string, error) {
	sessionID, csrfToken, apiHost, err := extractInitData(initData)
	if err != nil {
		return "", "", "", "", err
	}
	chatUUID, _ := initData["chat_uuid"].(string)
	if chatUUID == "" {
		return "", "", "", "", fmt.Errorf("missing chat_uuid in initialization data")
	}
	return sessionID, csrfToken, apiHost, chatUUID, nil
}

func extractUserInitData(initData map[string]interface{}) (string, string, string, string, error) {
	sessionID, csrfToken, apiHost, err := extractInitData(initData)
	if err != nil {
		return "", "", "", "", err
	}
	userID := extractUserID(initData["user_id"])
	if userID == "" {
		return "", "", "", "", fmt.Errorf("missing user_id in initialization data")
	}
	return sessionID, csrfToken, apiHost, userID, nil
}

func extractSupportTaskActionInitData(initData map[string]interface{}) (string, string, string, string, string, error) {
	sessionID, csrfToken, apiHost, err := extractInitData(initData)
	if err != nil {
		return "", "", "", "", "", err
	}
	taskPK, _ := initData["task_pk"].(string)
	if taskPK == "" {
		return "", "", "", "", "", fmt.Errorf("missing task_pk in initialization data")
	}
	actionID, _ := initData["action_id"].(string)
	if actionID == "" {
		return "", "", "", "", "", fmt.Errorf("missing action_id in initialization data")
	}
	return sessionID, csrfToken, apiHost, taskPK, actionID, nil
}

func extractPaperCategorizationInitData(initData map[string]interface{}) (string, string, string, error) {
	paperID, _ := initData["paper_id"].(string)
	apiHost, _ := initData["api_host"].(string)
	paperTitle, _ := initData["paper_title"].(string)
	if paperID == "" || apiHost == "" {
		return "", "", "", fmt.Errorf("missing required initialization data")
	}
	return paperID, apiHost, paperTitle, nil
}

func validateJSONResponse(responseBody []byte) error {
	responseStr := string(responseBody)
	if strings.TrimSpace(responseStr)[0] == '<' {
		return fmt.Errorf("received HTML response instead of JSON: %s", responseStr)
	}
	return nil
}

func extractJSONField(responseBody []byte, fieldName string) ([]byte, error) {
	if err := validateJSONResponse(responseBody); err != nil {
		return nil, err
	}
	var responseData map[string]interface{}
	if err := json.Unmarshal(responseBody, &responseData); err != nil {
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
