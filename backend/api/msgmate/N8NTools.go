package msgmate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

// --- N8N Trigger Workflow Webhook Tool ---

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
	Description:    "Trigger an n8n workflow webhook with custom input parameters",
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
		log.Printf("[N8NTool] Starting n8n_trigger_workflow_webhook tool execution")
		var webhookInput = input.(N8NTriggerWorkflowWebhookToolInput)

		// Log all initialization data for debugging
		log.Printf("[N8NTool] Init data keys: %v", getMapKeys(initData))

		// Verify the required initialization parameters
		apiEndpoint, ok := initData["api_endpoint"].(string)
		if !ok || apiEndpoint == "" {
			log.Printf("[N8NTool] Error: missing or invalid api_endpoint")
			return "", fmt.Errorf("missing or invalid api_endpoint in initialization data")
		}
		log.Printf("[N8NTool] API endpoint: %s", apiEndpoint)

		apiUser, ok := initData["api_user"].(string)
		if !ok || apiUser == "" {
			log.Printf("[N8NTool] Error: missing or invalid api_user")
			return "", fmt.Errorf("missing or invalid api_user in initialization data")
		}
		log.Printf("[N8NTool] API user: %s", apiUser)

		apiPassword, ok := initData["api_password"].(string)
		if !ok || apiPassword == "" {
			log.Printf("[N8NTool] Error: missing or invalid api_password")
			return "", fmt.Errorf("missing or invalid api_password in initialization data")
		}
		log.Printf("[N8NTool] API password: [REDACTED]")

		// Verify input parameters
		if webhookInput.InputParameters == nil {
			log.Printf("[N8NTool] Error: missing input_parameters")
			return "", fmt.Errorf("missing input_parameters in input data")
		}
		log.Printf("[N8NTool] Input parameters: %+v", webhookInput.InputParameters)

		// Prepare the request to the n8n webhook
		log.Printf("[N8NTool] Preparing to trigger n8n workflow webhook")

		// Marshal the input parameters to JSON
		bodyBytes, err := json.Marshal(webhookInput.InputParameters)
		if err != nil {
			log.Printf("[N8NTool] Error marshaling input parameters: %v", err)
			return "", fmt.Errorf("failed to marshal input parameters: %w", err)
		}
		log.Printf("[N8NTool] Request body: %s", string(bodyBytes))

		// Create a new request
		req, err := http.NewRequest("POST", apiEndpoint, bytes.NewBuffer(bodyBytes))
		if err != nil {
			log.Printf("[N8NTool] Error creating request: %v", err)
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		// Set the required headers
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(apiUser, apiPassword)

		log.Printf("[N8NTool] Request headers: Content-Type=%s, Authorization=Basic [REDACTED]",
			req.Header.Get("Content-Type"))

		// Send the request
		log.Printf("[N8NTool] Sending request to n8n webhook")
		client := &http.Client{
			Timeout: 30 * time.Second, // Set a reasonable timeout for webhook calls
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[N8NTool] Error sending request: %v", err)
			return "", fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		// Read and log the response body
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[N8NTool] Error reading response body: %v", err)
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		log.Printf("[N8NTool] API response status: %d", resp.StatusCode)
		// Only log a preview of the response body to avoid flooding logs
		if len(respBody) > 500 {
			log.Printf("[N8NTool] Response body preview: %s...", respBody[:500])
		} else {
			log.Printf("[N8NTool] Response body: %s", respBody)
		}

		// Check for successful response
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("[N8NTool] API returned non-success status: %d", resp.StatusCode)
			return "", fmt.Errorf("n8n webhook returned non-success status: %d, body: %s", resp.StatusCode, string(respBody))
		}

		// Try to parse the response as JSON for better formatting
		var responseData interface{}
		if err := json.Unmarshal(respBody, &responseData); err != nil {
			// If it's not JSON, return the raw response
			log.Printf("[N8NTool] Response is not JSON, returning raw response")
			log.Printf("[N8NTool] Tool execution completed successfully")
			return fmt.Sprintf("n8n workflow triggered successfully. Response: %s", string(respBody)), nil
		}

		// Format the JSON response nicely
		formattedResponse, err := json.MarshalIndent(responseData, "", "  ")
		if err != nil {
			log.Printf("[N8NTool] Error formatting JSON response: %v", err)
			return fmt.Sprintf("n8n workflow triggered successfully. Response: %s", string(respBody)), nil
		}

		log.Printf("[N8NTool] Tool execution completed successfully")
		return fmt.Sprintf("n8n workflow triggered successfully. Response:\n%s", string(formattedResponse)), nil
	},
}

func NewN8NTriggerWorkflowWebhookTool() Tool {
	return NewToolFromDefinition(N8NTriggerWorkflowWebhookToolDef)
}
