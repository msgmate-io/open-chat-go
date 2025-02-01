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
}

type ToolCallsResult struct {
	ToolCalls []ToolCall
}

func printToolDefinition(tool Tool) {
	fmt.Println("=== TOOL DEFINITION ===")
	fmt.Printf("Name: %s\n", tool.GetToolName())
	fmt.Printf("Type: %s\n", tool.GetToolType())
	fmt.Printf("Description: %s\n", tool.GetToolDescription())
	fmt.Printf("Requires Init: %v\n", tool.GetRequiresInit())

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

		currentMessages := messages
		maxRetries := 8 // Maximum number of tool call retries
		retryCount := 0
		processedToolIds := make(map[string]bool)            // Track processed tool IDs
		toolCallDetails := make([]map[string]interface{}, 0) // Track detailed tool call information

		// Track message content to avoid duplicate messages
		sentMessages := make(map[string]bool)
		aiResponseComplete := false

		for {
			// Check if we've exceeded max retries
			if retryCount >= maxRetries {
				errChan <- fmt.Errorf("exceeded maximum number of tool call retries (%d)", maxRetries)
				return
			}

			// Make initial request
			fmt.Println("\n=== STARTING NEW REQUEST ROUND ===")
			fmt.Printf("Current retry count: %d/%d\n", retryCount, maxRetries)
			toolCallResult, err := processStreamingRequest(
				host, model, backend, currentMessages, tools, toolMap, apiKey,
				chunkChan, usageChan, toolChan, errChan,
			)
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

							// Add retry information
							completionData["retry_count"] = retryCount
							completionData["max_retries"] = maxRetries

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

			// Check for duplicate tool calls by content
			if toolCallResult.arguments != "" {
				// Create a content signature based on tool name and arguments
				contentSignature := fmt.Sprintf("%s:%s", toolCallResult.toolName, toolCallResult.arguments)

				if sentMessages[contentSignature] {
					log.Printf("Warning: Duplicate tool call detected with content signature: %s, skipping", contentSignature)
					// Don't increment retry counter for duplicates
					continue
				}

				// Mark this content as sent
				sentMessages[contentSignature] = true
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
					"timestamp": time.Now().Format(time.RFC3339),
				}
				toolCallDetails = append(toolCallDetails, toolCallInfo)
			}

			fmt.Println("Called tool: ", toolCallResult.toolName, "with result: ", toolCallResult.result)

			// Increment retry counter when a tool is used
			retryCount++

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
			toolResultMsg := map[string]interface{}{
				"role":         "tool",
				"tool_call_id": toolCallResult.id,
				"content":      toolCallResult.result,
			}
			currentMessages = append(currentMessages, toolResultMsg)

			// Add a final assistant message to indicate completion
			finalMessage := map[string]interface{}{
				"role":    "assistant",
				"content": "I've processed your request using the " + toolCallResult.toolName + " tool.",
			}
			currentMessages = append(currentMessages, finalMessage)

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
	usedTool        bool
	id              string
	toolName        string
	toolCallMessage map[string]string
	result          string
	arguments       string
	err             error
	aiResponse      string
}

func processStreamingRequest(
	host, model, backend string,
	messages []map[string]interface{},
	tools []interface{},
	toolMap map[string]Tool,
	apiKey string,
	chunkChan chan<- string,
	usageChan chan<- *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	},
	toolChan chan<- ToolCall,
	errChan chan<- error,
) (*toolCallResult, error) {
	requestBody := map[string]interface{}{
		"model":    model,
		"messages": messages,
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

	result := &toolCallResult{}
	reader := bufio.NewReader(resp.Body)

	// Track the current tool call being built
	// fullMessage := ""
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

		data := strings.TrimPrefix(line, "data: ")
		data = strings.TrimSpace(data)

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

		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta

			// Handle regular content
			if delta.Content != "" {
				chunkChan <- delta.Content
				aiResponseBuilder.WriteString(delta.Content)
			}

			// Handle tool calls
			if len(delta.ToolCalls) > 0 {
				result.usedTool = true
				for _, tc := range delta.ToolCalls {
					toolCall, ok := tc.(map[string]interface{})
					if !ok {
						continue
					}

					// Get tool call ID
					if id, ok := toolCall["id"].(string); ok && id != "" {
						if id != currentToolCall.id {
							// New tool call - reset tracking
							result.id = id
							currentToolCall = struct {
								id        string
								name      string
								arguments string
							}{id: id}
						}
					}

					// Extract function details
					if function, ok := toolCall["function"].(map[string]interface{}); ok {
						// Get tool name
						if name, ok := function["name"].(string); ok && name != "" {
							currentToolCall.name = name
							result.toolName = name
						}
						// Accumulate arguments
						if args, ok := function["arguments"].(string); ok {
							currentToolCall.arguments += args
						}
					}

					// Try to parse complete tool call
					if currentToolCall.name != "" && currentToolCall.arguments != "" {
						tool, exists := toolMap[currentToolCall.name]
						if !exists {
							log.Printf("Warning: Tool '%s' not found in toolMap", currentToolCall.name)
							continue
						}

						if toolInput, err := tool.ParseArguments(currentToolCall.arguments); err == nil {
							fmt.Printf("\n=== EXECUTING TOOL: %s ===\n", currentToolCall.name)
							fmt.Printf("Tool ID: %s\n", currentToolCall.id)
							fmt.Printf("Arguments: %s\n", currentToolCall.arguments)

							// Set the tool call information in the result
							result.usedTool = true
							result.id = currentToolCall.id
							result.toolName = currentToolCall.name
							result.arguments = currentToolCall.arguments

							// Execute the tool and get the result
							toolResult, err := tool.RunTool(toolInput)
							if err != nil {
								result.err = err
								log.Printf("Error executing tool %s: %v", currentToolCall.name, err)
							} else {
								result.result = toolResult

								// Send tool call notification with complete information
								toolChan <- ToolCall{
									ToolName:  currentToolCall.name,
									ToolInput: toolInput,
									Id:        currentToolCall.id,
									Result:    toolResult,
								}
							}

							// Important: Break out of the loop after processing a complete tool call
							// This prevents processing the same tool call multiple times
							break
						}
					}
				}
			}
		}
	}

	// Set the captured AI response
	result.aiResponse = aiResponseBuilder.String()

	return result, nil
}
