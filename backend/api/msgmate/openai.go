package msgmate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// TokenUsage describes the token accounting returned by a provider for a
// single completion request.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ToolCall struct {
	ToolName  string
	ToolInput interface{}
	Id        string
	Result    string
	Status    string
	Error     string
	// StartedAtUnixMs is when execution of the call began (unix milliseconds),
	// and DurationMs is how long it took. They let the chat UI show a live timer
	// while a call runs and its duration once it completes (ci#569).
	StartedAtUnixMs int64
	DurationMs      int64
}

// IsTerminalToolCallStatus reports whether a tool-call status means the call
// will not produce any further timing. Only a terminal call reports a recorded
// duration, so an ongoing call keeps ticking instead of freezing at 0ms.
func IsTerminalToolCallStatus(status string) bool {
	switch status {
	case ToolCallStatusSucceeded, ToolCallStatusFailed, ToolCallStatusPendingConfirmation:
		return true
	default:
		return false
	}
}

// toolCallTimingFields builds the per-call timing fields the chat UI renders
// (ci#569): `started_at` lets the widget tick while the call runs, and
// `duration_ms` is the recorded execution time once the call finished.
//
// A completed call reports its duration even when it was zero — a cached
// (already executed) call finishes instantly — because the widget must show
// "0ms" rather than inventing an elapsed time from the start stamp. An ongoing
// call never reports a duration: its duration field is still zero and would
// freeze the live timer.
func toolCallTimingFields(status string, startedAtUnixMs, durationMs int64) map[string]interface{} {
	fields := map[string]interface{}{}
	if startedAtUnixMs > 0 {
		fields["started_at"] = startedAtUnixMs
	}
	if durationMs > 0 || (IsTerminalToolCallStatus(status) && startedAtUnixMs > 0) {
		fields["duration_ms"] = durationMs
	}
	return fields
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

	// Context-window retry (ci#394 gap 7). When a provider rejects a request for
	// exceeding the context window the tool loop trims the history and retries.
	// A single cut is often not enough for a long session and a cut that lands
	// inside a tool-call/tool-result pair yields a request the provider rejects
	// outright, so the retry shrinks in rounds down to a floor, repairing the
	// tool pairing after every cut and finally falling back to truncating
	// oversized message contents when the message count cannot be reduced
	// further.
	contextRetryShrinkRounds      = 4
	contextRetryTailFloorMessages = 4
	contextRetryMaxMessageChars   = 4000
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

func toolRequest(host string, model string, backend string, messages []map[string]string, tools []interface{}, apiKey string) (<-chan ToolCallsResult, <-chan *TokenUsage, <-chan error) {
	// Channel for tool calls result
	toolCallsChan := make(chan ToolCallsResult)
	// Channel for usage info
	usageChan := make(chan *TokenUsage)
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
			Usage *TokenUsage `json:"usage"`
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
	ctx context.Context,
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
	samplingParams SamplingParams,
	retryOpts ProviderRetryOptions,
	meta providerRequestMeta,
) (<-chan string, <-chan *TokenUsage, <-chan ToolCall, <-chan error) {
	chunkChan := make(chan string)
	usageChan := make(chan *TokenUsage)
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
		emptyContinuationRetried := false
		malformedInlineToolCallRetried := false
		var toolCallResult *toolCallResult
		var err error

		for {
			// A stop signal aborts the whole tool loop: once the context is
			// cancelled neither the provider request nor any further tool body
			// may run, so the already-dispatched turn cannot keep emitting
			// results after the user pressed stop.
			if ctxErr := ctx.Err(); ctxErr != nil {
				errChan <- ctxErr
				return
			}
			if totalToolCalls >= toolCallMaxTotal {
				errChan <- fmt.Errorf("exceeded maximum number of tool calls (%d)", toolCallMaxTotal)
				return
			}
			if failedToolCalls >= toolCallMaxFailed {
				errChan <- fmt.Errorf("exceeded maximum number of failed tool calls (%d)", toolCallMaxFailed)
				return
			}

			// Make initial request (with provider retries when enabled).
			fmt.Println("\n=== STARTING NEW REQUEST ROUND ===")
			fmt.Printf("Current tool-call counts: total=%d/%d failed=%d/%d\n", totalToolCalls, toolCallMaxTotal, failedToolCalls, toolCallMaxFailed)
			maxAttempts := effectiveProviderRetryAttempts(retryOpts)
			attempts := 0
			for {
				attempts++
				toolCallResult, err = processStreamingRequest(
					ctx, host, model, backend, currentMessages, tools, toolMap, apiKey,
					executedToolResults,
					chunkChan, usageChan, toolChan, errChan,
					samplingParams,
					providerRequestMeta{ChatUUID: meta.ChatUUID, TrackUsage: meta.TrackUsage, Attempt: attempts},
				)
				if err == nil {
					break
				}
				// A cancelled context is terminal: never retry (which would
				// re-run the generation after a stop).
				if ctx.Err() != nil {
					break
				}
				// Never retry mid-stream failures (they may have already
				// emitted partial output) or context-window errors (they have
				// their own trim-and-retry handling below).
				if isContextWindowExceededError(err) || !isPreStreamProviderFailure(err) || !isRetryableProviderRequestError(err) {
					break
				}
				if attempts >= maxAttempts {
					break
				}
				delay := providerRetryBackoffDelay(attempts, retryOpts.BaseBackoff, retryOpts.MaxBackoff)
				retryAt := time.Now().Add(delay)
				log.Printf(
					"Provider request failed (attempt %d/%d): %v; retrying in %s",
					attempts, maxAttempts, err, delay,
				)
				if retryOpts.OnRetryAttempt != nil {
					retryOpts.OnRetryAttempt(attempts, maxAttempts, err, retryAt)
				}
				// Wait for the backoff, but abandon it immediately if the user
				// stops the response.
				select {
				case <-ctx.Done():
				case <-time.After(delay):
				}
				if ctx.Err() != nil {
					err = ctx.Err()
					break
				}
			}
			// Record how many attempts were made so the final error message
			// can describe the retries.
			if preErr, ok := err.(*ProviderRequestError); ok {
				preErr.Attempts = attempts
			}
			if err != nil && ctx.Err() == nil && isContextWindowExceededError(err) {
				// Progressive shrink (ci#394 gap 7). Each round cuts a larger
				// tail (never the leading system messages, so an injected
				// compaction summary survives) and repairs the tool-call /
				// tool-result pairing so the retried request stays valid; when
				// the message count can no longer be reduced, oversized message
				// contents are truncated instead.
				for round := 0; round <= contextRetryShrinkRounds; round++ {
					shrunkMessages, shrunk := shrinkMessagesForContextRetry(currentMessages, round)
					if !shrunk {
						shrunkMessages, shrunk = shrinkOversizedContextMessages(currentMessages, contextRetryMaxMessageChars)
					}
					if !shrunk {
						break
					}
					log.Printf(
						"Context window exceeded; retrying with trimmed history (%d -> %d messages, round %d)",
						len(currentMessages),
						len(shrunkMessages),
						round,
					)
					currentMessages = shrunkMessages
					toolCallResult, err = processStreamingRequest(
						ctx, host, model, backend, currentMessages, tools, toolMap, apiKey,
						executedToolResults,
						chunkChan, usageChan, toolChan, errChan,
						samplingParams,
						providerRequestMeta{ChatUUID: meta.ChatUUID, TrackUsage: meta.TrackUsage, Attempt: attempts + 1},
					)
					if err == nil || ctx.Err() != nil || !isContextWindowExceededError(err) {
						break
					}
				}
			}
			// An inline tool-call block from which no call could be recovered is
			// intermittent provider corruption; retry the round once so the
			// model can re-emit a well-formed call instead of abandoning the
			// task (ci#428).
			if err != nil && ctx.Err() == nil && errors.Is(err, errMalformedInlineToolCall) && !malformedInlineToolCallRetried {
				malformedInlineToolCallRetried = true
				log.Printf("Model emitted an unrecoverable inline tool-call block; retrying once")
				continue
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

			// A post-confirmation continuation that produces neither text nor a
			// tool call is useless and is often a truncated provider emission;
			// retry it once before giving up so an intermittent blip does not
			// silently abandon the remaining task steps (ci#428). A round whose
			// only output was a leaked context-compaction transcript is in the
			// same class: the transcript was stripped (ci#562), so the turn has
			// no usable answer either and is worth exactly one retry.
			if aiResponseComplete && !emptyContinuationRetried &&
				strings.TrimSpace(toolCallResult.aiResponse) == "" &&
				(meta.PostConfirmationContinuation || toolCallResult.compactionOnly) {
				emptyContinuationRetried = true
				aiResponseComplete = false
				log.Printf(
					"Assistant round produced no usable text (post-confirmation continuation: %t, leaked compaction transcript: %t); retrying once",
					meta.PostConfirmationContinuation,
					toolCallResult.compactionOnly,
				)
				continue
			}

			// The retry after a leaked transcript produced another transcript:
			// the round has no answer and nothing left to retry. Report it as a
			// generation failure so the turn ends with a clear, human-readable
			// error (the work already done is kept) instead of finishing on an
			// empty message (ci#562).
			if aiResponseComplete && toolCallResult.compactionOnly &&
				strings.TrimSpace(toolCallResult.aiResponse) == "" && emptyContinuationRetried {
				errChan <- errLeakedCompactionTranscript
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

			// A round can surface a batch of tool calls. Feed every call of the
			// round back to the model, in emission order and attributed by its
			// call id, so the transcript the model sees is deterministic and
			// matches the calls it made — instead of only the last call of a
			// batch, which left the model guessing which results existed and in
			// what order (ci#655).
			appended, updatedMessages := appendExecutedToolCalls(currentMessages, toolCallResult.calls, processedToolIds)
			currentMessages = updatedMessages
			for _, call := range appended {
				toolCallDetails = append(toolCallDetails, map[string]interface{}{
					"id":        call.id,
					"name":      call.name,
					"arguments": call.arguments,
					"result":    call.result,
					"status":    call.status,
					"error":     call.error,
					"timestamp": time.Now().Format(time.RFC3339),
				})
				fmt.Println("Called tool: ", call.name, "with result: ", call.result)
				totalToolCalls++
				if call.status == ToolCallStatusFailed {
					failedToolCalls++
				}
			}

			// A round whose every call was already processed (or that surfaced no
			// usable call) would otherwise spin without adding anything to the
			// transcript: finish instead of re-requesting forever.
			if len(appended) == 0 {
				return
			}

			if toolCallResult.stopAfterTool {
				log.Printf("Stopping response after confirmable tool call: %s", toolCallResult.toolName)
				return
			}

			currentMessagesIndented, _ := json.MarshalIndent(currentMessages, "", "    ")
			fmt.Println("Current messages: ", string(currentMessagesIndented))

			// Instead, we'll continue the loop but with logging
			fmt.Println("Tool processing complete for this round. Continuing conversation...")
			continue
		}
	}()

	return chunkChan, usageChan, toolChan, errChan
}

// executedToolCall is one fully-resolved tool call of a single streaming round.
// A round may surface a batch of calls; they are kept in emission order so the
// caller can feed every result back to the model deterministically (ci#655).
type executedToolCall struct {
	id        string
	name      string
	arguments string
	result    string
	status    string
	error     string
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
	// compactionOnly records that the round's only output was a leaked
	// context-compaction transcript, which was stripped and must never be
	// persisted as the answer; the loop retries such a round once (ci#562).
	compactionOnly bool
	usage          *TokenUsage
	// calls holds every tool call of the round, in the order the model
	// emitted them (ci#655). The singular id/toolName/result/... fields above
	// mirror the last call for backwards compatibility.
	calls []executedToolCall
}

// appendExecutedToolCalls appends, for every executed call that has not already
// been processed, an assistant message carrying exactly that one tool call
// followed by its matching tool-result message — in the order the calls were
// made. That is the transcript the model reads back on the next round, so it
// must be complete, ordered and attributed by call id (ci#655). It returns the
// updated message list and the calls it actually appended, and marks each
// appended id in processed so a repeated id is never fed back twice.
//
// Keeping this in one place makes the "every call of the batch reaches the
// model, in order" contract unit-testable without a live provider.
func appendExecutedToolCalls(
	currentMessages []map[string]interface{},
	calls []executedToolCall,
	processed map[string]bool,
) ([]executedToolCall, []map[string]interface{}) {
	messages := currentMessages
	appended := make([]executedToolCall, 0, len(calls))
	for _, call := range calls {
		if call.id != "" && processed[call.id] {
			log.Printf("Warning: Tool ID %s has already been processed, skipping to avoid duplicate calls", call.id)
			continue
		}
		if call.id != "" {
			processed[call.id] = true
		}

		messages = append(messages, map[string]interface{}{
			"role":         "assistant",
			"tool_call_id": call.id,
			"content":      "",
			"tool_calls": []map[string]interface{}{
				{
					"type": "function",
					"id":   call.id,
					"function": map[string]interface{}{
						"arguments": call.arguments,
						"name":      call.name,
					},
				},
			},
		})

		modelToolResult := truncateToolResultForModel(call.result)
		if modelToolResult != call.result {
			log.Printf(
				"Truncated tool result for model context (%d -> %d chars): %s",
				len(call.result),
				len(modelToolResult),
				call.name,
			)
		}
		messages = append(messages, map[string]interface{}{
			"role":         "tool",
			"tool_call_id": call.id,
			"content":      modelToolResult,
		})
		appended = append(appended, call)
	}
	return appended, messages
}

func processStreamingRequest(
	ctx context.Context,
	host, model, backend string,
	messages []map[string]interface{},
	tools []interface{},
	toolMap map[string]Tool,
	apiKey string,
	executedToolResults map[string]string,
	chunkChan chan<- string,
	usageChan chan<- *TokenUsage,
	toolChan chan<- ToolCall,
	errChan chan<- error,
	samplingParams SamplingParams,
	meta providerRequestMeta,
) (*toolCallResult, error) {
	if backend == "testbackend" {
		reader, err := buildTestBackendStreamingReader(messages, toolMap)
		if err != nil {
			return nil, err
		}
		return processStreamingResponseReader(ctx, reader, toolMap, executedToolResults, chunkChan, usageChan, toolChan)
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
	applySamplingParamsToRequestBody(requestBody, samplingParams, backend, model)

	middlewareCtx := &ProviderRequestContext{
		ChatUUID:    meta.ChatUUID,
		Endpoint:    host,
		Backend:     backend,
		Model:       model,
		TrackUsage:  meta.TrackUsage,
		RequestBody: requestBody,
	}
	runProviderRequestMiddleware(middlewareCtx)

	requestStart := time.Now()
	reportOutcome := func(statusCode int, requestErr error, usage *TokenUsage, response map[string]interface{}) {
		reportProviderRequestResult(
			middlewareCtx,
			meta.Attempt,
			statusCode,
			requestErr,
			time.Since(requestStart),
			usage,
			response,
		)
	}

	// Setup request
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		reportOutcome(0, fmt.Errorf("failed to marshal request body: %w", err), nil, nil)
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/chat/completions", host), bytes.NewBuffer(jsonData))
	if err != nil {
		reportOutcome(0, fmt.Errorf("failed to create request: %w", err), nil, nil)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		reportOutcome(0, fmt.Errorf("request failed: %w", err), nil, nil)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		reportOutcome(resp.StatusCode, &ProviderRequestError{
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
			Attempts:   1,
		}, nil, nil)
		return nil, &ProviderRequestError{
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
			Attempts:   1,
		}
	}

	reader := bufio.NewReader(resp.Body)
	result, streamErr := processStreamingResponseReader(ctx, reader, toolMap, executedToolResults, chunkChan, usageChan, toolChan)

	var usage *TokenUsage
	var response map[string]interface{}
	if result != nil {
		usage = result.usage
		response = map[string]interface{}{
			"used_tool":       result.usedTool,
			"stop_after_tool": result.stopAfterTool,
		}
		if result.aiResponse != "" {
			response["content"] = result.aiResponse
		}
		if result.toolName != "" {
			toolCallRepr := map[string]interface{}{
				"id":        result.id,
				"name":      result.toolName,
				"arguments": result.arguments,
				"result":    result.result,
				"status":    result.status,
			}
			if strings.TrimSpace(result.error) != "" {
				toolCallRepr["error"] = result.error
			}
			response["tool_call"] = toolCallRepr
		}
	}
	reportOutcome(0, streamErr, usage, response)

	return result, streamErr
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

// trimMessagesForContextRetry trims the oldest turns once the provider has
// rejected a request for exceeding the context window. It preserves every
// leading system message (so an injected compaction summary survives) and
// returns false when the history cannot be reduced further.
func trimMessagesForContextRetry(messages []map[string]interface{}) ([]map[string]interface{}, bool) {
	return shrinkMessagesForContextRetry(messages, 0)
}

// contextRetryTailForRound halves the retained tail on every successive shrink
// round, down to a floor that still leaves the model enough recent turns to
// continue the work.
func contextRetryTailForRound(round int) int {
	tail := DefaultContextRetryTailMessages
	for i := 0; i < round; i++ {
		tail /= 2
		if tail <= contextRetryTailFloorMessages {
			return contextRetryTailFloorMessages
		}
	}
	return tail
}

// shrinkMessagesForContextRetry cuts the history back to round's tail size,
// keeping the leading system messages, and repairs the tool-call/tool-result
// pairing of the result. It reports false when the message count cannot be
// reduced (round 0), or when the cut would leave the history shorter than the
// caller can retry with.
func shrinkMessagesForContextRetry(messages []map[string]interface{}, round int) ([]map[string]interface{}, bool) {
	if round < 0 {
		round = 0
	}
	tail := contextRetryTailForRound(round)
	if len(messages) <= tail {
		return messages, false
	}

	leadingSystem := leadingSystemMessageCount(messages)
	tailStart := len(messages) - tail
	if tailStart < leadingSystem {
		tailStart = leadingSystem
	}
	if tailStart <= 0 {
		return messages, false
	}

	trimmed := make([]map[string]interface{}, 0, leadingSystem+tail)
	trimmed = append(trimmed, messages[:leadingSystem]...)
	trimmed = append(trimmed, messages[tailStart:]...)

	// A cut can land inside an assistant tool_calls message and its tool
	// results; the provider rejects such a history outright, so repair the
	// pairing before retrying with it.
	repaired, _ := repairToolCallPairing(trimmed)

	if len(repaired) >= len(messages) {
		return messages, false
	}
	if !hasConversationalTurn(repaired) {
		return messages, false
	}
	return repaired, true
}

// leadingSystemMessageCount counts the leading system messages of a request.
func leadingSystemMessageCount(messages []map[string]interface{}) int {
	count := 0
	for count < len(messages) {
		role, _ := messages[count]["role"].(string)
		if role != "system" {
			break
		}
		count++
	}
	return count
}

// hasConversationalTurn reports whether a message list still carries at least
// one non-system turn to answer.
func hasConversationalTurn(messages []map[string]interface{}) bool {
	for _, message := range messages {
		if role, _ := message["role"].(string); role != "system" {
			return true
		}
	}
	return false
}

// toolCallIDsOf returns the tool-call ids declared by an assistant message,
// tolerating both the []map[string]interface{} shape the loop builds and the
// []interface{} shape a decoded request carries.
func toolCallIDsOf(message map[string]interface{}) []string {
	raw, ok := message["tool_calls"]
	if !ok {
		return nil
	}
	ids := make([]string, 0, 2)
	appendCall := func(call map[string]interface{}) {
		id, _ := call["id"].(string)
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	switch calls := raw.(type) {
	case []map[string]interface{}:
		for _, call := range calls {
			appendCall(call)
		}
	case []interface{}:
		for _, entry := range calls {
			if call, ok := entry.(map[string]interface{}); ok {
				appendCall(call)
			}
		}
	}
	return ids
}

// toolCallIDOf returns the tool_call_id a tool-result message answers.
func toolCallIDOf(message map[string]interface{}) string {
	id, _ := message["tool_call_id"].(string)
	return strings.TrimSpace(id)
}

// repairToolCallPairing makes a message list acceptable to an OpenAI-compatible
// provider after an arbitrary cut:
//
//   - a declared tool call with no tool result anywhere in the list is removed
//     from its assistant message (an unanswered call is a hard provider error),
//   - an assistant message left with neither tool calls nor content is dropped,
//   - a tool result whose id was never declared *before* it (an orphaned result
//     from a mid-pair cut) is dropped.
//
// It returns the repaired list and whether anything changed. When nothing
// changed the original slice is returned unchanged.
func repairToolCallPairing(messages []map[string]interface{}) ([]map[string]interface{}, bool) {
	answered := map[string]bool{}
	for _, message := range messages {
		role, _ := message["role"].(string)
		if role != "tool" {
			continue
		}
		if id := toolCallIDOf(message); id != "" {
			answered[id] = true
		}
	}

	changed := false
	declared := map[string]bool{}
	out := make([]map[string]interface{}, 0, len(messages))

	for _, message := range messages {
		role, _ := message["role"].(string)
		switch role {
		case "assistant":
			ids := toolCallIDsOf(message)
			if len(ids) == 0 {
				out = append(out, message)
				continue
			}
			unanswered := false
			for _, id := range ids {
				if !answered[id] {
					unanswered = true
					break
				}
			}
			if !unanswered {
				for _, id := range ids {
					declared[id] = true
				}
				out = append(out, message)
				continue
			}
			// At least one declared call is unanswered: keep the answered ones.
			changed = true
			kept := keepAnsweredToolCalls(message["tool_calls"], answered)
			if len(kept) == 0 {
				if !messageHasContent(message) {
					continue // drop the empty assistant tool-call shell
				}
				stripped := make(map[string]interface{}, len(message))
				for key, value := range message {
					if key == "tool_calls" {
						continue
					}
					stripped[key] = value
				}
				out = append(out, stripped)
				continue
			}
			clone := make(map[string]interface{}, len(message))
			for key, value := range message {
				clone[key] = value
			}
			clone["tool_calls"] = kept
			for _, call := range kept {
				if id, _ := call["id"].(string); strings.TrimSpace(id) != "" {
					declared[strings.TrimSpace(id)] = true
				}
			}
			out = append(out, clone)
		case "tool":
			id := toolCallIDOf(message)
			if id == "" || !declared[id] {
				changed = true
				continue
			}
			out = append(out, message)
		default:
			out = append(out, message)
		}
	}

	if !changed {
		return messages, false
	}
	return out, true
}

// keepAnsweredToolCalls filters a message's tool_calls to the ones that have a
// matching tool result, preserving the original ordering and value shape.
func keepAnsweredToolCalls(raw interface{}, answered map[string]bool) []map[string]interface{} {
	kept := make([]map[string]interface{}, 0, 2)
	appendCall := func(call map[string]interface{}) {
		id, _ := call["id"].(string)
		if answered[strings.TrimSpace(id)] {
			kept = append(kept, call)
		}
	}
	switch calls := raw.(type) {
	case []map[string]interface{}:
		for _, call := range calls {
			appendCall(call)
		}
	case []interface{}:
		for _, entry := range calls {
			if call, ok := entry.(map[string]interface{}); ok {
				appendCall(call)
			}
		}
	}
	return kept
}

// messageHasContent reports whether a message carries non-empty content.
func messageHasContent(message map[string]interface{}) bool {
	switch content := message["content"].(type) {
	case string:
		return strings.TrimSpace(content) != ""
	case []map[string]interface{}:
		return len(content) > 0
	case []interface{}:
		return len(content) > 0
	}
	return false
}

// shrinkOversizedContextMessages is the last-resort context fallback: when the
// message count can no longer be reduced (for example a very long session whose
// history is already only a few huge tool results), truncate the oversized
// string contents instead. Leading system messages are never touched, so an
// injected compaction summary survives intact.
func shrinkOversizedContextMessages(messages []map[string]interface{}, maxChars int) ([]map[string]interface{}, bool) {
	if maxChars <= 0 {
		return messages, false
	}
	changed := false
	out := make([]map[string]interface{}, len(messages))
	copy(out, messages)
	for i, message := range out {
		if role, _ := message["role"].(string); role == "system" {
			continue
		}
		content, ok := message["content"].(string)
		if !ok || len(content) <= maxChars {
			continue
		}
		notice := fmt.Sprintf("\n\n[context retry: content truncated from %d to %d chars to fit the model context]", len(content), maxChars)
		clone := make(map[string]interface{}, len(message))
		for key, value := range message {
			clone[key] = value
		}
		clone["content"] = content[:maxChars] + notice
		out[i] = clone
		changed = true
	}
	if !changed {
		return messages, false
	}
	// A cut can also leave an orphaned tool result behind, so keep the retry
	// request valid.
	repaired, _ := repairToolCallPairing(out)
	if !hasConversationalTurn(repaired) {
		return messages, false
	}
	return repaired, true
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

// processParsedToolCall executes or surfaces a single fully parsed tool call,
// whether it came from a structured streaming delta or was recovered from a
// DeepSeek DSML block in the assistant content. It mirrors the structured
// path's confirmation/caching semantics and records the outcome on result. The
// caller is responsible for stopping the enclosing batch once result.stopAfterTool
// is set.
func processParsedToolCall(
	ctx context.Context,
	id string,
	name string,
	arguments string,
	toolMap map[string]Tool,
	executedToolResults map[string]string,
	result *toolCallResult,
	toolChan chan<- ToolCall,
) {
	if name == "" || arguments == "" {
		return
	}

	// The stop button cancels the generation context; never dispatch a tool
	// body after that, even if the provider stream still delivered the call.
	if ctxErr := ctx.Err(); ctxErr != nil {
		log.Printf("Skipping tool %s: generation cancelled", name)
		return
	}

	tool, exists := toolMap[name]
	if !exists {
		log.Printf("Warning: Tool '%s' not found in toolMap", name)
		return
	}

	toolInput, parseErr := tool.ParseArguments(arguments)
	if parseErr != nil {
		return
	}

	fmt.Printf("\n=== EXECUTING TOOL: %s ===\n", name)
	fmt.Printf("Tool ID: %s\n", id)
	fmt.Printf("Arguments: %s\n", arguments)

	contentSignature := fmt.Sprintf("%s:%s", name, strings.TrimSpace(arguments))
	if cachedResult, alreadyExecuted := executedToolResults[contentSignature]; alreadyExecuted {
		// Only successful calls are cached, so a cache hit is a repeat of a call
		// that already succeeded. Failures are deliberately not cached: the state
		// may change between two identical calls (e.g. a coding workspace becomes
		// selected after the first read failed), and replaying a stale error would
		// trap the model in a failure loop instead of letting it recover.
		log.Printf("Warning: Duplicate tool call detected with content signature: %s, reusing prior successful result", contentSignature)

		result.usedTool = true
		result.id = id
		result.toolName = name
		result.arguments = arguments
		result.result = cachedResult
		result.status = ToolCallStatusSucceeded
		result.error = ""
		result.calls = append(result.calls, executedToolCall{
			id:        id,
			name:      name,
			arguments: arguments,
			result:    cachedResult,
			status:    ToolCallStatusSucceeded,
		})

		toolChan <- ToolCall{
			ToolName:  name,
			ToolInput: toolInput,
			Id:        id,
			Result:    cachedResult,
			Status:    ToolCallStatusSucceeded,
			Error:     "",
			// A cache hit is also a completed call: report a zero-length
			// duration so the UI can render its timing row (ci#569).
			StartedAtUnixMs: time.Now().UnixMilli(),
		}
		return
	}

	result.usedTool = true
	result.id = id
	result.toolName = name
	result.arguments = arguments

	startedAtUnixMs := time.Now().UnixMilli()
	toolChan <- ToolCall{
		ToolName:        name,
		ToolInput:       toolInput,
		Id:              id,
		Result:          "",
		Status:          ToolCallStatusOngoing,
		StartedAtUnixMs: startedAtUnixMs,
	}

	var toolResult string
	status := ToolCallStatusSucceeded
	toolErr := ""
	requiresConfirmation, resolvedResult := tool.ResolveConfirmation(toolInput)
	switch {
	case requiresConfirmation:
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
	case resolvedResult != "":
		// A code-defined resolver (eg coder_ask in an autonomous session)
		// decided the call must not be confirmed and supplied the answer
		// itself; use it as the tool result and continue instead of pausing
		// for the user.
		toolResult = resolvedResult
		result.result = resolvedResult
		status = ToolCallStatusSucceeded
	default:
		var (
			executedResult string
			runErr         error
		)
		if contextTool, ok := tool.(ContextTool); ok {
			executedResult, runErr = contextTool.RunToolContext(ctx, toolInput)
		} else {
			executedResult, runErr = tool.RunTool(toolInput)
		}
		if runErr != nil {
			log.Printf("Error executing tool %s: %v", name, runErr)
			toolResult = buildToolErrorPlaceholder(name, runErr)
			result.result = toolResult
			status = ToolCallStatusFailed
			toolErr = runErr.Error()
		} else {
			toolResult = executedResult
			result.result = executedResult
			status = ToolCallStatusSucceeded
		}
	}

	toolChan <- ToolCall{
		ToolName:        name,
		ToolInput:       toolInput,
		Id:              id,
		Result:          toolResult,
		Status:          status,
		Error:           toolErr,
		StartedAtUnixMs: startedAtUnixMs,
		DurationMs:      time.Since(time.UnixMilli(startedAtUnixMs)).Milliseconds(),
	}
	result.status = status
	result.error = toolErr
	result.calls = append(result.calls, executedToolCall{
		id:        id,
		name:      name,
		arguments: arguments,
		result:    toolResult,
		status:    status,
		error:     toolErr,
	})
	// Cache only successful results. Caching failures would make an identical
	// retry (after the underlying state changed) replay the stale error instead
	// of re-executing the tool.
	if status == ToolCallStatusSucceeded {
		executedToolResults[contentSignature] = toolResult
	}
}

func processStreamingResponseReader(
	ctx context.Context,
	reader *bufio.Reader,
	toolMap map[string]Tool,
	executedToolResults map[string]string,
	chunkChan chan<- string,
	usageChan chan<- *TokenUsage,
	toolChan chan<- ToolCall,
) (*toolCallResult, error) {
	result := &toolCallResult{}

	// A single assistant round can stream several tool calls and the provider
	// interleaves their deltas: the `index` field identifies which call a delta
	// belongs to, one call's arguments arrive across several deltas, and deltas
	// for different calls can alternate. Reassemble each call by index (falling
	// back to its id) and only execute it once the stream ends, in emission
	// order, so every result is delivered exactly once, attributed to its call
	// id and in call order instead of being run as partial deltas arrive
	// (ci#655).
	type streamedCall struct {
		id        string
		name      string
		arguments string
	}
	var streamedKeys []string
	streamed := map[string]*streamedCall{}
	lastKey := ""
	streamedToolCallsSeen := false
	var aiResponseBuilder strings.Builder
	// inlineToolCallDetected records that the model emitted an inline tool-call
	// block (DeepSeek DSML or Anthropic/XML) in the content. Such content is
	// never user-facing: it is withheld from the client stream and recovered
	// (or reported) at the end.
	inlineToolCallDetected := false

	for {
		// A stop aborts the reader immediately: don't keep consuming the
		// provider stream (or dispatch its tool calls) after cancellation.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
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
			Usage *TokenUsage `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chunk: %w", err)
		}

		if chunk.Usage != nil {
			result.usage = chunk.Usage
			usageChan <- chunk.Usage
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			aiResponseBuilder.WriteString(delta.Content)
			accumulated := aiResponseBuilder.String()
			// Detect against the *accumulated* content: an inline call marker
			// (DeepSeek DSML, Anthropic/XML or the bracket placeholder some
			// models emit) can be split across deltas, and a marker recognised
			// only once it is complete would already have leaked its prefix to
			// the client and — worse — been stored as the assistant text
			// (ci#428, ci#562).
			if !inlineToolCallDetected && containsInlineToolCall(accumulated, toolMap) {
				inlineToolCallDetected = true
			}
			// A leaked context-compaction transcript must not reach the client
			// either: hold back content from the delta that opens a block (and
			// any still-ambiguous opening prefix of one, which releases again
			// as soon as the answer diverges). Prose forwarded before the block
			// started is genuine and stays.
			compactionSuspect := compactionBlockRe.MatchString(accumulated) || compactionOutputSuspected(accumulated)
			if !inlineToolCallDetected && !compactionSuspect {
				chunkChan <- delta.Content
			}
		}

		if len(delta.ToolCalls) == 0 {
			continue
		}

		streamedToolCallsSeen = true
		for _, tc := range delta.ToolCalls {
			toolCall, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}

			id, _ := toolCall["id"].(string)
			key := ""
			if index, hasIndex := toolCallIndexValue(toolCall); hasIndex {
				key = fmt.Sprintf("index:%d", index)
			} else if id != "" {
				key = "id:" + id
			} else {
				// A continuation delta without an index or id belongs to the
				// call that was opened last.
				key = lastKey
			}
			if key == "" {
				continue
			}
			call := streamed[key]
			if call == nil {
				call = &streamedCall{}
				streamed[key] = call
				streamedKeys = append(streamedKeys, key)
			}
			lastKey = key
			if id != "" {
				call.id = id
			}
			if function, ok := toolCall["function"].(map[string]interface{}); ok {
				if name, ok := function["name"].(string); ok && name != "" {
					call.name = name
				}
				if args, ok := function["arguments"].(string); ok {
					call.arguments += args
				}
			}
		}
	}

	result.aiResponse = aiResponseBuilder.String()

	// A leaked context-compaction transcript is model bookkeeping, never a
	// user-facing answer: strip it so it can never be stored as (or posted as)
	// the turn's reply, and flag the round when the transcript was its only
	// output so the tool loop retries once instead of finishing on it (ci#562).
	if sanitized, onlyBlob := sanitizeCompactionOutput(result.aiResponse); onlyBlob {
		result.aiResponse = sanitized
		result.compactionOnly = true
	}

	// Recover an inline tool-call block (DeepSeek DSML or Anthropic/XML) that
	// the provider did not map onto the structured tool_calls field. Without
	// this the raw fragment was stored as the assistant text and the tool loop
	// finished silently, abandoning the rest of a multi-step task (ci#428).
	if !streamedToolCallsSeen && inlineToolCallDetected {
		recovered := parseInlineToolCalls(result.aiResponse, toolMap)
		if len(recovered) == 0 {
			return result, errMalformedInlineToolCall
		}
		for i, call := range recovered {
			processParsedToolCall(
				ctx,
				fmt.Sprintf("inline-call-%d", i+1),
				call.ToolName,
				call.Arguments,
				toolMap,
				executedToolResults,
				result,
				toolChan,
			)
			if result.stopAfterTool {
				break
			}
		}
		// The raw inline fragment is not user-facing assistant text.
		result.aiResponse = ""
		return result, nil
	}

	// Execute the reassembled calls in emission order, each once. Only the
	// first stop-on-confirmable call is surfaced; the rest of the batch is
	// dropped so approving it does not re-apply already-applied mutations.
	for _, key := range streamedKeys {
		call := streamed[key]
		if call == nil || call.name == "" || call.arguments == "" {
			continue
		}
		result.usedTool = true
		processParsedToolCall(
			ctx,
			call.id,
			call.name,
			call.arguments,
			toolMap,
			executedToolResults,
			result,
			toolChan,
		)
		if result.stopAfterTool {
			log.Printf("Stopping stream parsing after first confirmable tool call: %s", result.toolName)
			break
		}
	}

	return result, nil
}

// toolCallIndexValue reads the OpenAI streaming `index` that identifies which
// tool call of a batched assistant turn a delta belongs to. Providers that do
// not emit it fall back to id-based attribution.
func toolCallIndexValue(toolCall map[string]interface{}) (int, bool) {
	switch v := toolCall["index"].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
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
