package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type N8NTriggerWorkflowWebhookToolInput struct {
	InputParameters map[string]interface{} `json:"input_parameters"`
}

type N8NTriggerWorkflowWebhookToolInit struct {
	ApiEndpoint string `json:"api_endpoint"`
	ApiUser     string `json:"api_user"`
	ApiPassword string `json:"api_password"`
}

var N8NTriggerWorkflowWebhookToolDef = ToolDefinition{
	Name:           "n8n_trigger_workflow_webhook",
	Description:    "Trigger an n8n workflow webhook with custom input parameters.",
	RequiresInit:   true,
	InputType:      N8NTriggerWorkflowWebhookToolInput{},
	RequiredParams: []string{"input_parameters"},
	Parameters: map[string]interface{}{
		"input_parameters": map[string]interface{}{
			"type":        "object",
			"description": "The input parameters to send to the n8n webhook as a JSON object",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		toolInput := input.(N8NTriggerWorkflowWebhookToolInput)
		apiEndpoint, _ := initData["api_endpoint"].(string)
		apiUser, _ := initData["api_user"].(string)
		apiPassword, _ := initData["api_password"].(string)
		if apiEndpoint == "" || apiUser == "" || apiPassword == "" {
			return "", fmt.Errorf("missing api_endpoint, api_user, or api_password in initialization data")
		}
		if toolInput.InputParameters == nil {
			return "", fmt.Errorf("missing input_parameters in input data")
		}

		bodyBytes, err := json.Marshal(toolInput.InputParameters)
		if err != nil {
			return "", fmt.Errorf("failed to marshal input parameters: %w", err)
		}
		req, err := http.NewRequest("POST", apiEndpoint, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(apiUser, apiPassword)

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read response body: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("n8n webhook returned non-success status: %d, body: %s", resp.StatusCode, string(respBody))
		}
		return fmt.Sprintf("n8n workflow triggered successfully. Response: %s", string(respBody)), nil
	},
}
