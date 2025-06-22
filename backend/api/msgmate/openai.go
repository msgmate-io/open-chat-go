package msgmate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type ToolCall struct {
	ToolName  string
	ToolInput interface{}
	Id        string
	Result    string
	Status    string
	Error     string
}

const (
	ToolCallStatusOngoing             = "ongoing"
	ToolCallStatusSucceeded           = "succeeded"
	ToolCallStatusFailed              = "failed"
	ToolCallStatusPendingConfirmation = "pending_confirmation"
	DefaultToolCallMaxTotal           = 12
	DefaultToolCallMaxFailed          = 3
	DefaultContextRetryTailMessages   = 14
	DefaultToolResultMaxCharsForModel = 12000
)

type ToolCallsResult struct {
	ToolCalls []ToolCall
}

func printToolDefinition(tool Tool) {
	fmt.Println("=== TOOL DEFINITION ===")
	fmt.Printf("Name: %s\n", tool.GetToolName())
	fmt.Printf("Type: %s\n", tool.GetToolType())
	fmt.Printf("Description: %s\n", tool.GetToolDescription())
	fmt.Printf("Requires Init: %v\n", tool.GetRequiresInit())
	fmt.Printf("Requires Confirmation: %v\n", tool.GetRequiresConfirmation())

	// Print parameters
	fmt.Println("Parameters:")
	params := tool.GetToolParameters()
	paramsJson, _ := json.MarshalIndent(params, "  ", "  ")
	fmt.Printf("  %s\n", string(paramsJson))

	// Print the constructed tool definition
	toolDef, _ := json.MarshalIndent(tool.ConstructTool(), "  ", "  ")
	fmt.Println("Full Tool Definition:")
	fmt.Printf("  %s\n", string(toolDef))
	fmt.Println("=====================")
}

func buildConfirmationSuggestion(toolName string, toolInput interface{}, continueAfterExecute bool) string {
	payload := map[string]interface{}{
		"type":                  "confirm-action",
		"requires_confirmation": true,
		"tool_name":             toolName,
		"target_tool_name":      toolName,
		"tool_input":            toolInput,
		"status":                "pending_confirmation",
	}
	if continueAfterExecute {
		payload["continue_after_execute"] = true
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		if continueAfterExecute {
			return "{\"type\":\"confirm-action\",\"requires_confirmation\":true,\"status\":\"pending_confirmation\",\"continue_after_execute\":true}"
		}
		return "{\"type\":\"confirm-action\",\"requires_confirmation\":true,\"status\":\"pending_confirmation\"}"
	}
	return string(encoded)
}

func buildToolErrorPlaceholder(toolName string, runErr error) string {
	if runErr == nil {
		return fmt.Sprintf("Tool %s failed. Please continue without tool output.", toolName)
	}
	return fmt.Sprintf(
		"Tool %s failed with error: %s. Please continue and provide the best possible response without this tool result.",
		toolName,
		runErr.Error(),
	)
}

func isConfirmActionPayload(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return false
	}
	typeValue, _ := payload["type"].(string)
	return typeValue == "confirm-action"
}

func toolRequest(host string, model string, backend string, messages []map[string]string, tools []interface{}, apiKey string) (<-chan ToolCallsResult, <-chan *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}, <-chan error) {
	// Channel for tool calls result
	toolCallsChan := make(chan ToolCallsResult)
	// Channel for usage info
	usageChan := make(chan *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	})
	// Channel for errors
	errChan := make(chan error, 1)

	go func() {
		defer close(toolCallsChan)
		defer close(usageChan)
		defer close(errChan)

		requestBody := map[string]interface{}{
			"model":    model,
			"messages": messages,
			"tools":    tools,
		}

		// Convert the request body to JSON
		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			errChan <- fmt.Errorf("failed to marshal request body: %w", err)
			return
		}

		// Create the HTTP request
		req, err := http.NewRequest(
			"POST",
			fmt.Sprintf("%s/chat/completions", host),
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			errChan <- fmt.Errorf("failed to create request: %w", err)
			return
		}

		// Set headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

		// Construct an HTTP client with a timeout
		client := &http.Client{Timeout: 30 * time.Second}

		// Perform the request
		resp, err := client.Do(req)
		if err != nil {
			errChan <- fmt.Errorf("request failed: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			errChan <- fmt.Errorf("non-200 response: %d %s",
				resp.StatusCode, string(bodyBytes))
			return
		}

		// Pretty print response body for debugging
		bodyBytes, _ := io.ReadAll(resp.Body)
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, bodyBytes, "", "    "); err == nil {
			fmt.Printf("Response body:\n%s\n", prettyJSON.String())
		}

		// Create new reader from the body bytes for further processing
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Read and parse the response
		var response struct {
			Choices []struct {
				Message struct {
					ToolCalls []struct {
						Function struct {
							Name      string          `json:"name"`
							Arguments json.RawMessage `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			errChan <- fmt.Errorf("failed to decode response: %w", err)
			return
		}

		// Send usage information if present
		if response.Usage != nil {
			log.Printf("Token usage - Prompt: %d, Completion: %d, Total: %d",
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				response.Usage.TotalTokens)
			usageChan <- response.Usage
		}

		// Extract tool calls from the response
		var result ToolCallsResult
		if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
			for _, toolCall := range response.Choices[0].Message.ToolCalls {
				var toolInput interface{}
				if err := json.Unmarshal(toolCall.Function.Arguments, &toolInput); err != nil {
					errChan <- fmt.Errorf("failed to parse tool arguments: %w", err)
					return
				}

				result.ToolCalls = append(result.ToolCalls, ToolCall{
					ToolName:  toolCall.Function.Name,
					ToolInput: toolInput,
				})
			}
		}

		toolCallsChan <- result
	}()

	return toolCallsChan, usageChan, errChan
}

// streamChatCompletion demonstrates how to make a streaming request to OpenAI's
// chat/completions endpoint. It returns two channels:
//  1. chunks: a channel of text chunks that arrive from the stream
//  2. usage: a channel for usage information (if any occur)
//  3. errs: a channel for errors (if any occur)
func streamChatCompletion(
	host string,
	model string,
	backend string,
	toolCallMaxTotal int,
	toolCallMaxFailed int,
	messages []map[string]interface{},
	tools []interface{},
	toolMap map[string]Tool,
	apiKey string,
	interactionStartTools []string,
	interactionCompleteTools []string,
	handler *MsgmateHandler,
) (<-chan string, <-chan *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}, <-chan ToolCall, <-chan error) {
	chunkChan := make(chan string)
	usageChan := make(chan *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	})
	toolChan := make(chan ToolCall)
	errChan := make(chan error, 1)

	go func() {
		defer close(chunkChan)
		defer close(usageChan)
		defer close(toolChan)
		defer close(errChan)

		// Execute interaction_start tools at the beginning
		if len(interactionStartTools) > 0 {
			log.Printf("Executing %d interaction_start tools", len(interactionStartTools))
			for _, toolName := range interactionStartTools {
				// Extract the actual tool name after the prefix
				actualToolName := strings.TrimPrefix(toolName, "interaction_start:")
				log.Printf("Executing interaction_start tool: %s %s", toolName, actualToolName)

				// Execute the function using the handler if available
				if handler != nil {
					tool, exists := toolMap[toolName]
					if !exists {
						log.Printf("Warning: Tool '%s' not found in toolMap", actualToolName)
						continue
					}

					toolResult, err := tool.RunTool(map[string]interface{}{})
					if err != nil {
						log.Printf("Error executing interaction_start tool %s: %v", actualToolName, err)
						continue
					}
					log.Printf("Successfully executed interaction_start tool %s with result: %v", actualToolName, toolResult)
				} else {
					log.Printf("No handler available, would execute interaction_start tool: %s", actualToolName)
				}
			}
		}

		// Add verbose logging of all tools
		fmt.Println("=== REGISTERED TOOLS ===")
		for toolName, tool := range toolMap {
			fmt.Printf("Tool registered: %s\n", toolName)
			printToolDefinition(tool)
		}
		fmt.Println("=======================")

		if toolCallMaxTotal < 1 {
			toolCallMaxTotal = DefaultToolCallMaxTotal
		}
		if toolCallMaxFailed < 1 {
			toolCallMaxFailed = DefaultToolCallMaxFailed
		}

		currentMessages := messages
		totalToolCalls := 0
		failedToolCalls := 0
		processedToolIds := make(map[string]bool)            // Track processed tool IDs
		toolCallDetails := make([]map[string]interface{}, 0) // Track detailed tool call information
		executedToolResults := make(map[string]string)
		aiResponseComplete := false

		for {
			if totalToolCalls >= toolCallMaxTotal {
				errChan <- fmt.Errorf("exceeded maximum number of tool calls (%d)", toolCallMaxTotal)
				return
			}
			if failedToolCalls >= toolCallMaxFailed {
				errChan <- fmt.Errorf("exceeded maximum number of failed tool calls (%d)", toolCallMaxFailed)
				return
			}

			// Make initial request
			fmt.Println("\n=== STARTING NEW REQUEST ROUND ===")
			fmt.Printf("Current tool-call counts: total=%d/%d failed=%d/%d\n", totalToolCalls, toolCallMaxTotal, failedToolCalls, toolCallMaxFailed)
			toolCallResult, err := processStreamingRequest(
				host, model, backend, currentMessages, tools, toolMap, apiKey,
				executedToolResults,
				chunkChan, usageChan, toolChan, errChan,
			)
			if err != nil && isContextWindowExceededError(err) {
				trimmedMessages, trimmed := trimMessagesForContextRetry(currentMessages)
				if trimmed {
					log.Printf(
						"Context window exceeded; retrying with trimmed history (%d -> %d messages)",
						len(currentMessages),
						len(trimmedMessages),
					)
					currentMessages = trimmedMessages
					toolCallResult, err = processStreamingRequest(
						host, model, backend, currentMessages, tools, toolMap, apiKey,
						executedToolResults,
						chunkChan, usageChan, toolChan, errChan,
					)
				}
			}
			if err != nil {
				errChan <- err
				return
			}

			// If no tool was used, the AI has finished its response
			if !toolCallResult.usedTool {
				aiResponseComplete = true
			}

			// If we encountered an error, we're done
			if toolCallResult.err != nil {
				log.Printf("Error: %v", toolCallResult.err)
				return
			}

			// If AI response is complete and no more tools are being called, execute completion tools
			if aiResponseComplete {
				// Execute interaction_complete tools before finishing
				if len(interactionCompleteTools) > 0 {
					log.Printf("Executing %d interaction_complete tools", len(interactionCompleteTools))
					for _, toolName := range interactionCompleteTools {
						// Extract the actual tool name after the prefix
						actualToolName := strings.TrimPrefix(toolName, "interaction_complete:")
						log.Printf("Executing interaction_complete tool: %s", actualToolName)

						// Execute the function using the handler if available
						if handler != nil {
							tool, exists := toolMap[toolName]
							if !exists {
								log.Printf("Warning: Tool '%s' not found in toolMap", actualToolName)
								continue
							}

							// Prepare detailed completion data
							completionData := map[string]interface{}{
								"completed": true,
								"timestamp": time.Now().Format(time.RFC3339),
							}

							// Add information about tools that were called
							if len(processedToolIds) > 0 {
								toolsCalled := make([]string, 0, len(processedToolIds))
								for toolId := range processedToolIds {
									toolsCalled = append(toolsCalled, toolId)
								}
								completionData["tools_called"] = toolsCalled
								completionData["tools_count"] = len(toolsCalled)
							}

							// Add detailed tool call information
							if len(toolCallDetails) > 0 {
								completionData["tool_call_details"] = toolCallDetails
							}

							// Add the actual AI response content from the toolCallResult
							if toolCallResult.aiResponse != "" {
								completionData["last_ai_message"] = toolCallResult.aiResponse
								completionData["last_message_role"] = "assistant"
							} else {
								// Fallback to last message in conversation if no AI response captured
								if len(currentMessages) > 0 {
									lastMessage := currentMessages[len(currentMessages)-1]
									if content, ok := lastMessage["content"].(string); ok && content != "" {
										completionData["last_ai_message"] = content
									}
									if role, ok := lastMessage["role"].(string); ok {
										completionData["last_message_role"] = role
									}
								}
							}

							completionData["tool_call_total_count"] = totalToolCalls
							completionData["tool_call_failed_count"] = failedToolCalls
							completionData["tool_call_max_total"] = toolCallMaxTotal
							completionData["tool_call_max_failed"] = toolCallMaxFailed

							toolResult, err := tool.RunTool(completionData)
							if err != nil {
								log.Printf("Error executing interaction_complete tool %s: %v", actualToolName, err)
								continue
							}
							log.Printf("Successfully executed interaction_complete tool %s with result: %v", actualToolName, toolResult)
						} else {
							log.Printf("No handler available, would execute interaction_complete tool: %s", actualToolName)
						}
					}
				}
				return
			}

			// Check if we've already processed this tool ID
			if toolCallResult.id != "" && processedToolIds[toolCallResult.id] {
				log.Printf("Warning: Tool ID %s has already been processed, skipping to avoid duplicate calls", toolCallResult.id)
				continue
			}

			// Mark this tool ID as processed
			if toolCallResult.id != "" {
				processedToolIds[toolCallResult.id] = true
			}

			// Track detailed tool call information
			if toolCallResult.usedTool && toolCallResult.id != "" {
				toolCallInfo := map[string]interface{}{
					"id":        toolCallResult.id,
					"name":      toolCallResult.toolName,
					"arguments": toolCallResult.arguments,
					"result":    toolCallResult.result,
					"status":    toolCallResult.status,
					"error":     toolCallResult.error,
					"timestamp": time.Now().Format(time.RFC3339),
				}
				toolCallDetails = append(toolCallDetails, toolCallInfo)
			}

			fmt.Println("Called tool: ", toolCallResult.toolName, "with result: ", toolCallResult.result)

			if toolCallResult.stopAfterTool {
				log.Printf("Stopping response after confirmable tool call: %s", toolCallResult.toolName)
				return
			}

			totalToolCalls++
			if toolCallResult.status == ToolCallStatusFailed {
				failedToolCalls++
			}

			// Add the tool call to the message history
			toolsCallMessage := map[string]interface{}{
				"role":         "assistant",
				"tool_call_id": toolCallResult.id,
				"content":      "",
				"tool_calls": []map[string]interface{}{
					{
						"type": "function",
						"id":   toolCallResult.id,
						"function": map[string]interface{}{
							"arguments": toolCallResult.arguments,
							"name":      toolCallResult.toolName,
						},
					},
				},
			}
			currentMessages = append(currentMessages, toolsCallMessage)

			// Add tool result to messages and continue conversation
			modelToolResult := truncateToolResultForModel(toolCallResult.result)
			if modelToolResult != toolCallResult.result {
				log.Printf(
					"Truncated tool result for model context (%d -> %d chars): %s",
					len(toolCallResult.result),
					len(modelToolResult),
					toolCallResult.toolName,
				)
			}
			toolResultMsg := map[string]interface{}{
				"role":         "tool",
				"tool_call_id": toolCallResult.id,
				"content":      modelToolResult,
			}
			currentMessages = append(currentMessages, toolResultMsg)

			currentMessagesIndented, _ := json.MarshalIndent(currentMessages, "", "    ")
			fmt.Println("Current messages: ", string(currentMessagesIndented))

			// Instead, we'll continue the loop but with logging
			fmt.Println("Tool processing complete for this round. Continuing conversation...")
			continue
		}
	}()

	return chunkChan, usageChan, toolChan, errChan
}

type toolCallResult struct {
	usedTool          bool
	stopAfterTool     bool
	duplicateToolCall bool
	id                string
	toolName          string
	toolCallMessage   map[string]string
	result            string
	arguments         string
	status            string
	error             string
	err               error
	aiResponse        string
}

func processStreamingRequest(
	host, model, backend string,
	messages []map[string]interface{},
	tools []interface{},
	toolMap map[string]Tool,
	apiKey string,
	executedToolResults map[string]string,
	chunkChan chan<- string,
	usageChan chan<- *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	},
	toolChan chan<- ToolCall,
	errChan chan<- error,
) (*toolCallResult, error) {
	if backend == "testbackend" {
		reader, err := buildTestBackendStreamingReader(messages, toolMap)
		if err != nil {
			return nil, err
		}
		return processStreamingResponseReader(reader, toolMap, executedToolResults, chunkChan, usageChan, toolChan)
	}

	normalizedMessages := normalizeMessagesForBackend(messages, backend)

	requestBody := map[string]interface{}{
		"model":    model,
		"messages": normalizedMessages,
		"stream":   true,
	}
	if len(tools) > 0 {
		requestBody["tools"] = tools
	}
	if backend == "openai" {
		requestBody["stream_options"] = map[string]interface{}{"include_usage": true}
	}

	// Setup request
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/chat/completions", host), bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("non-200 response: %d %s", resp.StatusCode, string(bodyBytes))
	}

	reader := bufio.NewReader(resp.Body)
	return processStreamingResponseReader(reader, toolMap, executedToolResults, chunkChan, usageChan, toolChan)
}

func normalizeMessagesForBackend(messages []map[string]interface{}, backend string) []map[string]interface{} {
	if backend != "anthropic" {
		return messages
	}

	if len(messages) == 0 {
		return messages
	}

	normalized := make([]map[string]interface{}, len(messages))
	copy(normalized, messages)

	for len(normalized) > 0 {
		last := normalized[len(normalized)-1]
		lastRole, _ := last["role"].(string)
		if lastRole != "assistant" {
			break
		}
		normalized = normalized[:len(normalized)-1]
	}

	if len(normalized) == 0 {
		return messages
	}

	return normalized
}

func isContextWindowExceededError(err error) bool {
	if err == nil {
		return false
	}
	errLower := strings.ToLower(err.Error())
	return strings.Contains(errLower, "contextwindowexceeded") ||
		strings.Contains(errLower, "maximum context length") ||
		strings.Contains(errLower, "context length")
}

func trimMessagesForContextRetry(messages []map[string]interface{}) ([]map[string]interface{}, bool) {
	if len(messages) <= DefaultContextRetryTailMessages {
		return messages, false
	}

	hasLeadingSystemMessage := false
	if len(messages) > 0 {
		role, _ := messages[0]["role"].(string)
		hasLeadingSystemMessage = role == "system"
	}

	tailStart := len(messages) - DefaultContextRetryTailMessages
	if hasLeadingSystemMessage && tailStart < 1 {
		tailStart = 1
	}
	if !hasLeadingSystemMessage && tailStart < 0 {
		tailStart = 0
	}

	trimmed := make([]map[string]interface{}, 0, DefaultContextRetryTailMessages+1)
	if hasLeadingSystemMessage {
		trimmed = append(trimmed, messages[0])
	}
	trimmed = append(trimmed, messages[tailStart:]...)

	if len(trimmed) >= len(messages) {
		return messages, false
	}

	return trimmed, true
}

func truncateToolResultForModel(result string) string {
	if len(result) <= DefaultToolResultMaxCharsForModel {
		return result
	}

	notice := fmt.Sprintf("\n\n[tool output truncated to %d chars out of %d to fit model context]", DefaultToolResultMaxCharsForModel, len(result))
	maxContentLen := DefaultToolResultMaxCharsForModel - len(notice)
	if maxContentLen < 0 {
		maxContentLen = 0
	}

	return result[:maxContentLen] + notice
}

func processStreamingResponseReader(
	reader *bufio.Reader,
	toolMap map[string]Tool,
	executedToolResults map[string]string,
	chunkChan chan<- string,
	usageChan chan<- *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	},
	toolChan chan<- ToolCall,
) (*toolCallResult, error) {
	result := &toolCallResult{}

	var currentToolCall struct {
		id        string
		name      string
		arguments string
	}
	var aiResponseBuilder strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed reading response: %w", err)
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string        `json:"content"`
					ToolCalls []interface{} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chunk: %w", err)
		}

		if chunk.Usage != nil {
			usageChan <- chunk.Usage
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			chunkChan <- delta.Content
			aiResponseBuilder.WriteString(delta.Content)
		}

		if len(delta.ToolCalls) == 0 {
			continue
		}

		result.usedTool = true
		for _, tc := range delta.ToolCalls {
			toolCall, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}

			if id, ok := toolCall["id"].(string); ok && id != "" {
				if id != currentToolCall.id {
					result.id = id
					currentToolCall = struct {
						id        string
						name      string
						arguments string
					}{id: id}
				}
			}

			if function, ok := toolCall["function"].(map[string]interface{}); ok {
				if name, ok := function["name"].(string); ok && name != "" {
					currentToolCall.name = name
					result.toolName = name
				}
				if args, ok := function["arguments"].(string); ok {
					currentToolCall.arguments += args
				}
			}

			if currentToolCall.name == "" || currentToolCall.arguments == "" {
				continue
			}

			tool, exists := toolMap[currentToolCall.name]
			if !exists {
				log.Printf("Warning: Tool '%s' not found in toolMap", currentToolCall.name)
				continue
			}

			toolInput, parseErr := tool.ParseArguments(currentToolCall.arguments)
			if parseErr != nil {
				continue
			}

			fmt.Printf("\n=== EXECUTING TOOL: %s ===\n", currentToolCall.name)
			fmt.Printf("Tool ID: %s\n", currentToolCall.id)
			fmt.Printf("Arguments: %s\n", currentToolCall.arguments)

			contentSignature := fmt.Sprintf("%s:%s", currentToolCall.name, strings.TrimSpace(currentToolCall.arguments))
			if cachedResult, alreadyExecuted := executedToolResults[contentSignature]; alreadyExecuted {
				log.Printf("Warning: Duplicate tool call detected with content signature: %s, reusing prior result", contentSignature)

				status := ToolCallStatusSucceeded
				toolErr := ""
				if strings.Contains(strings.ToLower(cachedResult), " failed with error:") {
					status = ToolCallStatusFailed
					toolErr = "duplicate tool call reused prior failure result"
				}

				result.usedTool = true
				result.id = currentToolCall.id
				result.toolName = currentToolCall.name
				result.arguments = currentToolCall.arguments
				result.result = cachedResult
				result.status = status
				result.error = toolErr

				toolChan <- ToolCall{
					ToolName:  currentToolCall.name,
					ToolInput: toolInput,
					Id:        currentToolCall.id,
					Result:    cachedResult,
					Status:    status,
					Error:     toolErr,
				}
				break
			}

			result.usedTool = true
			result.id = currentToolCall.id
			result.toolName = currentToolCall.name
			result.arguments = currentToolCall.arguments

			toolChan <- ToolCall{
				ToolName:  currentToolCall.name,
				ToolInput: toolInput,
				Id:        currentToolCall.id,
				Result:    "",
				Status:    ToolCallStatusOngoing,
			}

			var toolResult string
			status := ToolCallStatusSucceeded
			toolErr := ""
			if tool.GetRequiresConfirmation() {
				continueAfterExecute := tool.GetStopOnFirstConfirmableToolCall()
				toolResult = buildConfirmationSuggestion(tool.GetToolName(), toolInput, continueAfterExecute)
				status = ToolCallStatusPendingConfirmation

				modelResult := toolResult
				if blockMessage := strings.TrimSpace(tool.GetConfirmationBlockMessage()); blockMessage != "" {
					modelResult = blockMessage
				}
				result.result = modelResult
				if tool.GetStopOnFirstConfirmableToolCall() {
					result.stopAfterTool = true
				}
			} else {
				executedResult, runErr := tool.RunTool(toolInput)
				if runErr != nil {
					log.Printf("Error executing tool %s: %v", currentToolCall.name, runErr)
					toolResult = buildToolErrorPlaceholder(currentToolCall.name, runErr)
					result.result = toolResult
					status = ToolCallStatusFailed
					toolErr = runErr.Error()
				} else {
					toolResult = executedResult
					result.result = toolResult
					status = ToolCallStatusSucceeded
				}
			}

			toolChan <- ToolCall{
				ToolName:  currentToolCall.name,
				ToolInput: toolInput,
				Id:        currentToolCall.id,
				Result:    toolResult,
				Status:    status,
				Error:     toolErr,
			}
			result.status = status
			result.error = toolErr
			executedToolResults[contentSignature] = toolResult

			break
		}
	}

	result.aiResponse = aiResponseBuilder.String()
	return result, nil
}

func buildTestBackendStreamingReader(
	messages []map[string]interface{},
	toolMap map[string]Tool,
) (*bufio.Reader, error) {
	toolID := fmt.Sprintf("testbackend-tool-%d", time.Now().UnixNano())

	hasToolResult := false
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if role == "tool" {
			hasToolResult = true
			break
		}
		if role == "user" {
			break
		}
	}

	chunkPayloads := make([]map[string]interface{}, 0)
	if hasToolResult {
		chunkPayloads = append(chunkPayloads,
			map[string]interface{}{
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{"content": "<think>I should inspect the tool output before replying.</think>"},
				}},
			},
			map[string]interface{}{
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{"content": "The tool returned: Request completed successfully."},
				}},
				"usage": map[string]interface{}{"prompt_tokens": 32, "completion_tokens": 12, "total_tokens": 44},
			},
		)
	} else {
		selectedToolName := ""
		for _, candidate := range []string{"get_current_time_confirmed_testing", "get_current_time_confirmed", "get_current_time", "get_random_number_seeded", "get_random_number"} {
			if _, exists := toolMap[candidate]; exists {
				selectedToolName = candidate
				break
			}
		}

		if selectedToolName == "" {
			chunkPayloads = append(chunkPayloads,
				map[string]interface{}{
					"choices": []map[string]interface{}{{
						"index": 0,
						"delta": map[string]interface{}{"content": "testbackend mock response: no tool call required."},
					}},
					"usage": map[string]interface{}{"prompt_tokens": 9, "completion_tokens": 8, "total_tokens": 17},
				},
			)
		} else {
			toolArguments := "{}"
			switch selectedToolName {
			case "get_random_number", "get_random_number_seeded":
				toolArguments = `{"min":1,"max":100}`
			}

			chunkPayloads = append(chunkPayloads,
				map[string]interface{}{
					"choices": []map[string]interface{}{{
						"index": 0,
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{{
								"id":   toolID,
								"type": "function",
								"function": map[string]interface{}{
									"name":      selectedToolName,
									"arguments": toolArguments,
								},
							}},
						},
					}},
				},
				map[string]interface{}{
					"choices": []map[string]interface{}{{
						"index":         0,
						"delta":         map[string]interface{}{},
						"finish_reason": "tool_calls",
					}},
					"usage": map[string]interface{}{"prompt_tokens": 24, "completion_tokens": 6, "total_tokens": 30},
				},
			)
		}
	}

	var sseBuilder strings.Builder
	for _, payload := range chunkPayloads {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("testbackend failed to encode mock stream payload: %w", err)
		}
		sseBuilder.WriteString("data: ")
		sseBuilder.WriteString(string(encoded))
		sseBuilder.WriteString("\n\n")
	}
	sseBuilder.WriteString("data: [DONE]\n\n")

	return bufio.NewReader(strings.NewReader(sseBuilder.String())), nil
}
