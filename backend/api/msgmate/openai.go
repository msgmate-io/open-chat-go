package msgmate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// streamChatCompletion demonstrates how to make a streaming request to OpenAI's
// chat/completions endpoint. It returns two channels:
//  1. chunks: a channel of text chunks that arrive from the stream
//  2. errs: a channel for errors (if any occur)
func streamChatCompletion(host string, model string, messages []map[string]string) (<-chan string, <-chan error) {
	// We’ll send our partial text over this channel:
	chunkChan := make(chan string)
	// We’ll send any errors over this channel:
	errChan := make(chan error, 1)

	// Launch the request in a goroutine so the caller can keep receiving chunks.
	go func() {
		defer close(chunkChan)
		defer close(errChan)

		requestBody := map[string]interface{}{
			"model":    model,
			"messages": messages,
			"stream":   true, // enable streaming
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
			fmt.Sprintf("%s/v1/chat/completions", host),
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			errChan <- fmt.Errorf("failed to create request: %w", err)
			return
		}

		// Set headers.
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer YOUR_API_KEY")

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
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				errChan <- fmt.Errorf("failed to unmarshal chunk: %w", err)
				return
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
	return chunkChan, errChan
}
