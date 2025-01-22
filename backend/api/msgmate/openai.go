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

func toolRequest(host string, model string, backend string, messages []map[string]string, apiKey string) (<-chan ToolCallsResult, <-chan *struct {
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
func streamChatCompletion(host string, model string, backend string, messages []map[string]string, apiKey string) (<-chan string, <-chan *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}, <-chan error) {
	// We'll send our partial text over this channel:
	chunkChan := make(chan string)
	// We'll send usage info over this channel:
	usageChan := make(chan *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	})
	// We'll send any errors over this channel:
	errChan := make(chan error, 1)

	// Launch the request in a goroutine so the caller can keep receiving chunks.
	go func() {
		defer close(chunkChan)
		defer close(usageChan)
		defer close(errChan)

		requestBody := map[string]interface{}{
			"model":    model,
			"messages": messages,
			"stream":   true, // enable streaming
		}

		if backend == "openai" {
			requestBody["stream_options"] = map[string]interface{}{"include_usage": true}
		}

		// Convert the request body to JSON.
		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			errChan <- fmt.Errorf("failed to marshal request body: %w", err)
			return
		}

		// Create the HTTP request.
		req, err := http.NewRequest(
			"POST",
			fmt.Sprintf("%s/chat/completions", host),
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			errChan <- fmt.Errorf("failed to create request: %w", err)
			return
		}

		// Set headers.
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

		// Construct an HTTP client with a timeout (for safety).
		client := &http.Client{Timeout: 300 * time.Second}

		// Perform the request.
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

		// Read the response stream line by line.
		reader := bufio.NewReader(resp.Body)

		for {
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				errChan <- fmt.Errorf("failed reading response: %w", err)
				return
			}

			// Each streamed line starts with "data: ".
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			// Remove the "data: " prefix.
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)

			// Check for [DONE] sentinel
			if data == "[DONE]" {
				// Stream is finished
				return
			}

			// Unmarshal the JSON chunk.
			var chunk struct {
				ID      string `json:"id"`
				Object  string `json:"object"`
				Created int64  `json:"created"`
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"delta"`
					FinishReason interface{} `json:"finish_reason"`
					Index        int         `json:"index"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				errChan <- fmt.Errorf("failed to unmarshal chunk: %w", err)
				return
			}

			// Log usage information if present (usually in the last chunk)
			if chunk.Usage != nil {
				log.Printf("Token usage - Prompt: %d, Completion: %d, Total: %d",
					chunk.Usage.PromptTokens,
					chunk.Usage.CompletionTokens,
					chunk.Usage.TotalTokens)
				usageChan <- chunk.Usage
			} else {
				log.Printf("No token usage information found in chunk")
			}

			// Extract the content if available.
			if len(chunk.Choices) > 0 {
				content := chunk.Choices[0].Delta.Content
				if content != "" {
					// Send this chunk to the caller via the channel.
					chunkChan <- content
				}
			}
		}
	}()

	// Return the channels so the caller can listen for chunks and/or errors.
	return chunkChan, usageChan, errChan
}
