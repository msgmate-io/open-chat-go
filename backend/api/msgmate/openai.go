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
}

type ToolCallsResult struct {
	ToolCalls []ToolCall
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
	apiKey string,
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

		currentMessages := messages
		for {
			// Make initial request
			toolCallResult, err := processStreamingRequest(
				host, model, backend, currentMessages, tools, apiKey,
				chunkChan, usageChan, toolChan, errChan,
			)
			if err != nil {
				errChan <- err
				return
			}

			fmt.Println("Called tool: ", toolCallResult.toolName, "with result: ", toolCallResult.result)

			// If no tool was used or we encountered an error, we're done
			if !toolCallResult.usedTool || toolCallResult.err != nil {
				return
			}

			toolsCallMessage := map[string]interface{}{
				"role":         "assistant",
				"tool_call_id": toolCallResult.id,
				"content":      "",
				"tool_calls": []map[string]interface{}{
					{
						"type": "function",
						"id":   toolCallResult.id,
						"function": map[string]interface{}{
							"arguments": toolCallResult.result,
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

			currentMessagesIndented, _ := json.MarshalIndent(currentMessages, "", "    ")
			fmt.Println("Current messages: ", string(currentMessagesIndented))
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
	err             error
}

func processStreamingRequest(
	host, model, backend string,
	messages []map[string]interface{},
	tools []interface{},
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
						var toolsTmp Tool = NewWeatherTool()
						if toolInput, err := toolsTmp.ParseArguments(currentToolCall.arguments); err == nil {
							toolChan <- ToolCall{
								ToolName:  currentToolCall.name,
								ToolInput: toolInput,
							}
							result.result = "The weather in Paris is sunny" // TODO call the actual tool
						}
					}
				}
			}
		}
	}

	return result, nil
}
