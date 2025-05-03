package msgmate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	wsapi "backend/api/websocket"
	"backend/client"
)

func buildConfirmableActionFromToolCall(toolCall map[string]interface{}) map[string]interface{} {
	confirmation, ok := toolCall["confirmation"].(map[string]interface{})
	if !ok {
		return nil
	}

	targetToolName, _ := confirmation["target_tool_name"].(string)
	if targetToolName == "" {
		targetToolName, _ = toolCall["name"].(string)
	}

	actionID, _ := toolCall["id"].(string)
	action := map[string]interface{}{
		"action_id":             actionID,
		"source_tool_name":      toolCall["name"],
		"target_tool_name":      targetToolName,
		"status":                "pending",
		"requires_confirmation": true,
		"created_at":            time.Now().UTC().Format(time.RFC3339),
	}

	if suggested, exists := confirmation["suggested_inputs"]; exists {
		action["input"] = suggested
	}
	if continueAfterExecute, ok := confirmation["continue_after_execute"].(bool); ok {
		action["continue_after_execute"] = continueAfterExecute
	}
	if title, ok := confirmation["title"].(string); ok && title != "" {
		action["title"] = title
	}
	if description, ok := confirmation["description"].(string); ok && description != "" {
		action["description"] = description
	}
	if confirmLabel, ok := confirmation["confirm_label"].(string); ok && confirmLabel != "" {
		action["confirm_label"] = confirmLabel
	}
	if dangerLevel, ok := confirmation["danger_level"].(string); ok && dangerLevel != "" {
		action["danger_level"] = dangerLevel
	}

	return action
}

func parseConfirmActionPayload(raw string) (map[string]interface{}, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, false
	}
	typeValue, _ := payload["type"].(string)
	if typeValue != "confirm-action" {
		return nil, false
	}
	return payload, true
}

func collectConfirmableActions(toolCalls []interface{}) []interface{} {
	actions := make([]interface{}, 0)
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]interface{})
		if !ok {
			continue
		}
		action := buildConfirmableActionFromToolCall(toolCall)
		if action != nil {
			actions = append(actions, action)
		}
	}
	return actions
}

// AIHandlerImpl implements the AIHandler interface
type AIHandlerImpl struct {
	botContext *BotContext
}

// NewAIHandler creates a new AI handler
func NewAIHandler(botContext *BotContext) *AIHandlerImpl {
	return &AIHandlerImpl{
		botContext: botContext,
	}
}

// GenerateResponse generates an AI response for a message
func (aih *AIHandlerImpl) GenerateResponse(ctx context.Context, message wsapi.NewMessage) error {
	startTime := time.Now()
	var thinkingTime time.Duration
	var thinkingStart time.Time

	// 1 - first check if its a command or a plain text message
	if strings.HasPrefix(message.Content.Text, "/") {
		command := strings.Replace(message.Content.Text, "/", "", 1)
		return aih.ProcessCommand(ctx, command, message)
	}

	// Load the chat and its current configuration
	err, chat := aih.botContext.Client.GetChat(message.Content.ChatUUID)
	if err != nil {
		return err
	}

	// Extract configuration
	configMap := make(map[string]interface{})
	if chat.Config != nil {
		if m, ok := chat.Config.(map[string]interface{}); ok {
			configMap = m
		}
	}

	endpoint := mapGetOrDefault[string](configMap, "endpoint", "http://localai:8080")
	backend := mapGetOrDefault[string](configMap, "backend", "deepinfra")
	model := mapGetOrDefault[string](configMap, "model", "meta-llama-3.1-8b-instruct")
	reasoning := mapGetOrDefault[bool](configMap, "reasoning", false)
	context := mapGetOrDefault[int64](configMap, "context", 10)
	tools := mapGetOrDefault[[]string](configMap, "tools", []string{})
	toolInit := mapGetOrDefault[map[string]interface{}](configMap, "tool_init", map[string]interface{}{})
	dynamicTools := mapGetOrDefault[map[string]interface{}](configMap, "dynamic_tools", map[string]interface{}{})
	mcpTools := mapGetOrDefault[map[string]interface{}](configMap, "mcp_tools", map[string]interface{}{})
	systemPrompt := mapGetOrDefault[string](configMap, "system_prompt", "You are a helpful assistant.")
	tags := mapGetOrDefault[[]string](configMap, "tags", []string{})

	if backend == "litellm" {
		litellmHost := strings.TrimSpace(os.Getenv("LITELLM_API_HOST"))
		if litellmHost != "" {
			endpoint = litellmHost
		}
		endpoint = strings.TrimSpace(endpoint)
		endpoint = strings.TrimRight(endpoint, "/")
		if endpoint == "" {
			return fmt.Errorf("missing API host for litellm provider")
		}
	}

	if backend == "anthropic" {
		anthropicHost := strings.TrimSpace(os.Getenv("ANTHROPIC_API_HOST"))
		if anthropicHost != "" {
			endpoint = anthropicHost
		} else if strings.TrimSpace(endpoint) == "" {
			endpoint = "https://api.anthropic.com/v1"
		}
		endpoint = strings.TrimSpace(endpoint)
		endpoint = strings.TrimRight(endpoint, "/")
		if endpoint == "" {
			return fmt.Errorf("missing API host for anthropic provider")
		}
	}

	// Check for skip-core tag
	if aih.hasSkipCoreTag(tags) {
		log.Printf("Chat %s has skip-core tag, executing tools only", message.Content.ChatUUID)
		return aih.executeToolsOnly(ctx, message, tools, toolInit, dynamicTools, mcpTools)
	}

	// Load the past messages
	err, paginatedMessages := aih.botContext.Client.GetMessages(message.Content.ChatUUID, 1, context)
	if err != nil {
		return err
	}

	// Build OpenAI messages
	openAiMessages := aih.buildOpenAIMessages(&paginatedMessages, message, systemPrompt, backend)

	// Setup tools
	toolsData, toolMap, interactionStartTools, interactionCompleteTools := aih.setupTools(tools, toolInit, dynamicTools, mcpTools)

	// Add run_callback_function to tools
	tools = append(tools, "run_callback_function")

	// Log the extracted interaction tools for debugging
	if len(interactionStartTools) > 0 {
		log.Printf("Found interaction_start tools: %v", interactionStartTools)
	}
	if len(interactionCompleteTools) > 0 {
		log.Printf("Found interaction_complete tools: %v", interactionCompleteTools)
	}

	// Stream chat completion
	chunks, usage, toolCalls, errs := streamChatCompletion(
		endpoint,
		model,
		backend,
		openAiMessages,
		toolsData,
		toolMap,
		aih.botContext.Client.GetApiKey(backend),
		interactionStartTools,
		interactionCompleteTools,
		GetGlobalMsgmateHandler(),
	)

	// Process the streaming response
	return aih.processStreamingResponse(ctx, message, chunks, usage, toolCalls, errs, startTime, thinkingTime, thinkingStart, reasoning)
}

// ProcessCommand processes bot commands (like /pong, /loop)
func (aih *AIHandlerImpl) ProcessCommand(ctx context.Context, command string, message wsapi.NewMessage) error {
	if strings.HasPrefix(command, "pong") {
		aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
			Text: fmt.Sprintf("PONG '%s' ", command),
		})
		return nil
	} else if strings.HasPrefix(command, "loop") {
		var timeSlept float32 = 0.0
		for {
			aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text: fmt.Sprintf("LOOP '%f' ", timeSlept),
			})
			time.Sleep(1 * time.Second)
			timeSlept += 1
			if timeSlept > 10 {
				break
			}
		}
		return nil
	}
	return fmt.Errorf("unknown command '%s'", command)
}

// buildOpenAIMessages builds the OpenAI messages array from chat history
func (aih *AIHandlerImpl) buildOpenAIMessages(paginatedMessages *client.PaginatedMessages, message wsapi.NewMessage, systemPrompt, backend string) []map[string]interface{} {
	openAiMessages := []map[string]interface{}{}
	currentMessageIncluded := false

	// Add system prompt
	openAiMessages = append(openAiMessages, map[string]interface{}{"role": "system", "content": systemPrompt})

	// Process historical messages
	for i := len(paginatedMessages.Rows) - 1; i >= 0; i-- {
		msg := paginatedMessages.Rows[i]

		if msg.DataType == "event" {
			continue
		}
		if msg.MetaData != nil {
			if eventType, ok := (*msg.MetaData)["event_type"].(string); ok && eventType == "confirmable_action_execute" {
				continue
			}
		}

		// Check if message has attachments
		var attachments []interface{}
		if msg.MetaData != nil {
			if attData, ok := (*msg.MetaData)["attachments"]; ok {
				if attList, ok := attData.([]interface{}); ok {
					attachments = attList
				}
			}
		}

		if msg.SenderUUID == aih.botContext.Client.User.UUID {
			if msg.ToolCalls != nil && len(*msg.ToolCalls) > 0 {
				toolCallsPayload := make([]map[string]interface{}, 0, len(*msg.ToolCalls))
				for _, rawToolCall := range *msg.ToolCalls {
					toolCallMap, ok := rawToolCall.(map[string]interface{})
					if !ok {
						continue
					}
					toolCallID, _ := toolCallMap["id"].(string)
					toolName, _ := toolCallMap["name"].(string)
					if toolCallID == "" || toolName == "" {
						continue
					}

					argumentsText := "{}"
					switch typedArguments := toolCallMap["arguments"].(type) {
					case string:
						if strings.TrimSpace(typedArguments) != "" {
							argumentsText = typedArguments
						}
					default:
						if encodedArguments, err := json.Marshal(typedArguments); err == nil {
							argumentsText = string(encodedArguments)
						}
					}

					toolCallsPayload = append(toolCallsPayload, map[string]interface{}{
						"type": "function",
						"id":   toolCallID,
						"function": map[string]interface{}{
							"name":      toolName,
							"arguments": argumentsText,
						},
					})
				}

				if len(toolCallsPayload) > 0 {
					openAiMessages = append(openAiMessages, map[string]interface{}{
						"role":       "assistant",
						"content":    "",
						"tool_calls": toolCallsPayload,
					})

					for _, rawToolCall := range *msg.ToolCalls {
						toolCallMap, ok := rawToolCall.(map[string]interface{})
						if !ok {
							continue
						}
						toolCallID, _ := toolCallMap["id"].(string)
						if toolCallID == "" {
							continue
						}

						resultText := ""
						switch typedResult := toolCallMap["result"].(type) {
						case string:
							resultText = typedResult
						default:
							if encodedResult, err := json.Marshal(typedResult); err == nil {
								resultText = string(encodedResult)
							}
						}

						if strings.TrimSpace(resultText) == "" {
							continue
						}

						openAiMessages = append(openAiMessages, map[string]interface{}{
							"role":         "tool",
							"tool_call_id": toolCallID,
							"content":      resultText,
						})
					}
				}
			}

			if strings.TrimSpace(msg.Text) != "" {
				openAiMessages = append(openAiMessages, map[string]interface{}{"role": "assistant", "content": msg.Text})
			}
		} else {
			// Handle user messages with potential attachments
			contentArray := aih.processMessageAttachments(msg.Text, attachments, backend)
			openAiMessages = append(openAiMessages, map[string]interface{}{
				"role":    "user",
				"content": contentArray,
			})
		}
		if msg.Text == message.Content.Text {
			currentMessageIncluded = true
		}
	}

	// Add current message if not already included
	if !currentMessageIncluded {
		var attachments []interface{}
		if message.Content.Attachments != nil {
			// Convert FileAttachment slice to interface{} slice
			for _, att := range *message.Content.Attachments {
				attachments = append(attachments, map[string]interface{}{
					"file_id":   att.FileID,
					"mime_type": att.MimeType,
				})
			}
		}
		if strings.TrimSpace(message.Content.Text) != "" || len(attachments) > 0 {
			contentArray := aih.processCurrentMessageAttachments(message.Content.Text, attachments, backend)
			openAiMessages = append(openAiMessages, map[string]interface{}{
				"role":    "user",
				"content": contentArray,
			})
		}
	}

	return openAiMessages
}

// processMessageAttachments processes attachments for historical messages
func (aih *AIHandlerImpl) processMessageAttachments(text string, attachments []interface{}, backend string) interface{} {
	if len(attachments) > 0 {
		// Create content array with text and file references
		contentArray := []map[string]interface{}{}

		// Add text content if it exists
		if text != "" {
			contentArray = append(contentArray, map[string]interface{}{
				"type": "text",
				"text": text,
			})
		}

		// Process file attachments
		fileHandler := NewFileHandler(aih.botContext)
		processedAttachments, err := fileHandler.ProcessAttachments(attachments, backend)
		if err != nil {
			log.Printf("Error processing attachments: %v", err)
		} else {
			contentArray = append(contentArray, processedAttachments...)
		}

		return contentArray
	} else {
		return text
	}
}

// processCurrentMessageAttachments processes attachments for the current message
func (aih *AIHandlerImpl) processCurrentMessageAttachments(text string, attachments []interface{}, backend string) interface{} {
	if len(attachments) > 0 {
		// Create content array with text and file references
		contentArray := []map[string]interface{}{}

		// Add text content if it exists
		if text != "" {
			contentArray = append(contentArray, map[string]interface{}{
				"type": "text",
				"text": text,
			})
		}

		// Process file attachments
		fileHandler := NewFileHandler(aih.botContext)
		processedAttachments, err := fileHandler.ProcessAttachments(attachments, backend)
		if err != nil {
			log.Printf("Error processing attachments: %v", err)
		} else {
			contentArray = append(contentArray, processedAttachments...)
		}

		return contentArray
	} else {
		return text
	}
}

// setupTools sets up the tools for the AI response
func (aih *AIHandlerImpl) setupTools(tools []string, toolInit map[string]interface{}, dynamicTools map[string]interface{}, mcpTools map[string]interface{}) ([]interface{}, map[string]Tool, []string, []string) {
	var toolsData []interface{}
	toolMap := map[string]Tool{}
	var interactionStartTools []string
	var interactionCompleteTools []string

	if len(tools) > 0 {
		// Debug: Print all available tools in AllTools
		log.Printf("=== DEBUG: Available tools in AllTools ===")
		for i, tool := range AllTools {
			log.Printf("AllTools[%d]: %s (RequiresInit: %v)", i, tool.GetToolName(), tool.GetRequiresInit())
		}
		log.Printf("=== END DEBUG ===")

		for _, toolName := range tools {
			actualToolName := toolName
			if strings.HasPrefix(toolName, "interaction_start:") {
				interactionStartTools = append(interactionStartTools, toolName)
				parts := strings.SplitN(toolName, ":", 2)
				if len(parts) == 2 {
					actualToolName = parts[1]
				}
			} else if strings.HasPrefix(toolName, "interaction_complete:") {
				interactionCompleteTools = append(interactionCompleteTools, toolName)
				parts := strings.SplitN(toolName, ":", 2)
				if len(parts) == 2 {
					actualToolName = parts[1]
				}
			}
			log.Printf("====> Processing tool registration for %s, with actual tool name %s", toolName, actualToolName)

			// Always instantiate a fresh tool per interaction. Mutating entries from AllTools
			// would share tool_init (e.g. task_pk) across concurrent interactions.
			tool, toolFound := NewToolByName(actualToolName)
			if actualToolName == "run_callback_function" {
				tool = NewRunCallbackFunctionTool()
				toolFound = true
			}
			if !toolFound || tool == nil {
				dynamicTool, dynamicFound, dynamicErr := NewDynamicRESTToolFromSnapshot(actualToolName, dynamicTools)
				if dynamicErr != nil {
					log.Printf("====> WARNING: Dynamic tool %s failed to load: %v", actualToolName, dynamicErr)
					continue
				}
				if dynamicFound && dynamicTool != nil {
					tool = dynamicTool
				} else {
					mcpTool, mcpFound, mcpErr := NewMCPToolFromSnapshot(actualToolName, mcpTools)
					if mcpErr != nil {
						log.Printf("====> WARNING: MCP tool %s failed to load: %v", actualToolName, mcpErr)
						continue
					}
					if !mcpFound || mcpTool == nil {
						log.Printf("====> WARNING: Tool %s NOT FOUND!", actualToolName)
						continue
					}
					tool = mcpTool
				}
			}
			log.Printf("====> Registered tool instance %s (RequiresInit: %v)", actualToolName, tool.GetRequiresInit())
			toolsData = append(toolsData, tool.ConstructTool())
			toolMap[toolName] = tool
			toolMap[tool.GetToolFunctionName()] = tool
			if tool.GetRequiresInit() {
				initData, ok := toolInit[toolName]
				if !ok {
					initData, ok = toolInit[actualToolName]
				}
				if ok {
					log.Printf("Setting init data for tool %s (toolName: %s)", tool.GetToolName(), toolName)
					tool.SetInitData(initData)
				} else {
					log.Printf("Tool init data not found for tool %s (toolName: %s)", tool.GetToolName(), toolName)
					tool.SetInitData(map[string]interface{}{})
				}
			}
		}
	}

	return toolsData, toolMap, interactionStartTools, interactionCompleteTools
}

// processStreamingResponse processes the streaming response from the AI
func (aih *AIHandlerImpl) processStreamingResponse(ctx context.Context, message wsapi.NewMessage, chunks <-chan string, usage <-chan *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}, toolCalls <-chan ToolCall, errs <-chan error, startTime time.Time, thinkingTime time.Duration, thinkingStart time.Time, reasoning bool) error {
	var allToolCalls []interface{}
	var processedToolCallIds = make(map[string]bool) // Track processed tool call IDs
	var fullText, thoughtBuffer, currentThoughtStep strings.Builder
	var reasoningEntries []string
	var thinkingSteps []map[string]string
	var isThinking bool
	const thinkStartTag = "<think>"
	const thinkEndTag = "</think>"
	var hadToolCall bool
	var currentBuffer strings.Builder
	partialSessionID := fmt.Sprintf("%s-%d", message.Content.ChatUUID, time.Now().UnixNano())
	thinkTagPattern := regexp.MustCompile(`(?is)<think>(.*?)</think>`)
	var tokenUsage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}

	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.StartPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
		),
	)

	finalizePartial := func() {
		aih.botContext.WSHandler.MessageHandler.SendMessage(
			aih.botContext.WSHandler,
			message.Content.SenderUUID,
			aih.botContext.WSHandler.MessageHandler.EndPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
				partialSessionID,
			),
		)
	}

	appendThoughtEntries := func(entries []string) {
		for _, entry := range entries {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			alreadyExists := false
			for _, existing := range reasoningEntries {
				if existing == trimmed {
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				reasoningEntries = append(reasoningEntries, trimmed)
			}
		}
	}

	finalizeThinkingStep := func(duration time.Duration) {
		stepText := strings.TrimSpace(currentThoughtStep.String())
		if stepText == "" {
			currentThoughtStep.Reset()
			return
		}
		appendThoughtEntries([]string{stepText})
		thinkingSteps = append(thinkingSteps, map[string]string{
			"text":     stepText,
			"duration": duration.Round(time.Millisecond).String(),
		})
		currentThoughtStep.Reset()
	}

	extractThinkSections := func(raw string) (string, []string) {
		if strings.TrimSpace(raw) == "" {
			return "", nil
		}

		matches := thinkTagPattern.FindAllStringSubmatchIndex(raw, -1)
		if len(matches) == 0 {
			if strings.Contains(raw, thinkEndTag) {
				parts := strings.Split(raw, thinkEndTag)
				if len(parts) > 1 {
					orphanThought := strings.ReplaceAll(parts[0], thinkStartTag, "")
					cleanTail := strings.Join(parts[1:], thinkEndTag)
					cleanTail = strings.ReplaceAll(cleanTail, thinkStartTag, "")
					cleanTail = strings.ReplaceAll(cleanTail, thinkEndTag, "")
					for strings.Contains(cleanTail, "\n\n\n") {
						cleanTail = strings.ReplaceAll(cleanTail, "\n\n\n", "\n\n")
					}
					return strings.TrimSpace(cleanTail), []string{orphanThought}
				}
			}
			clean := raw
			clean = strings.ReplaceAll(clean, thinkStartTag, "")
			clean = strings.ReplaceAll(clean, thinkEndTag, "")
			for strings.Contains(clean, "\n\n\n") {
				clean = strings.ReplaceAll(clean, "\n\n\n", "\n\n")
			}
			return strings.TrimSpace(clean), nil
		}

		thoughts := make([]string, 0, len(matches))
		var cleanBuilder strings.Builder
		lastIdx := 0
		for _, match := range matches {
			startIdx, endIdx := match[0], match[1]
			contentStart, contentEnd := match[2], match[3]
			if startIdx > lastIdx {
				cleanBuilder.WriteString(raw[lastIdx:startIdx])
			}
			thoughts = append(thoughts, raw[contentStart:contentEnd])
			lastIdx = endIdx
		}
		if lastIdx < len(raw) {
			cleanBuilder.WriteString(raw[lastIdx:])
		}

		clean := cleanBuilder.String()
		for strings.Contains(clean, "\n\n\n") {
			clean = strings.ReplaceAll(clean, "\n\n\n", "\n\n")
		}
		return strings.TrimSpace(clean), thoughts
	}

	// Helper function to send final message and cleanup
	sendFinalMessage := func(isCancelled bool, streamErr error) {
		// If we're still thinking when finishing, add the final thinking time
		totalTime := time.Since(startTime)

		finalizePartial()

		text, extractedThoughts := extractThinkSections(fullText.String())
		appendThoughtEntries(extractedThoughts)
		if isCancelled {
			text += "\nI paused this response. Send another message when you want me to continue."
			if reasoning {
				reasoningEntries = append(reasoningEntries, "Response paused.")
			}
		}
		if streamErr != nil {
			text = strings.TrimSpace(text)
			if text == "" {
				text = "I ran into an error while generating a reply. Please try again in a moment."
			} else {
				text += "\n\nI ran into an error while finishing this reply."
			}
			if reasoning {
				reasoningEntries = append(reasoningEntries, "Response stopped due to an upstream provider error.")
			}
		}

		metadata := map[string]interface{}{
			"total_time": totalTime.Round(time.Millisecond).String(),
			"cancelled":  isCancelled,
			"finished":   true,
		}
		if streamErr != nil {
			metadata["error"] = true
			metadata["error_detail"] = streamErr.Error()
		}
		confirmableActions := collectConfirmableActions(allToolCalls)
		if len(confirmableActions) > 0 {
			metadata["confirmable_actions"] = confirmableActions
		}
		if tokenUsage != nil {
			metadata["token_usage"] = tokenUsage
		}
		if reasoning {
			metadata["thinking_time"] = thinkingTime.Round(time.Millisecond).String()
			if len(thinkingSteps) > 0 {
				metadata["thinking_steps"] = thinkingSteps
			}
		}

		if len(reasoningEntries) > 0 {
			aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text:      text,
				Reasoning: reasoningEntries,
				MetaData:  &metadata,
				ToolCalls: &allToolCalls,
			})
		} else {
			aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text:      text,
				MetaData:  &metadata,
				ToolCalls: &allToolCalls,
			})
		}
	}

	currentThinkingDuration := func() time.Duration {
		if !isThinking {
			return thinkingTime
		}
		return thinkingTime + time.Since(thinkingStart)
	}

	sendRespondingChunk := func(chunk string) {
		if chunk == "" {
			return
		}
		fullText.WriteString(chunk)
		totalTime := time.Since(startTime)
		if reasoning {
			thinkingElapsed := currentThinkingDuration()
			aih.botContext.WSHandler.MessageHandler.SendMessage(
				aih.botContext.WSHandler,
				message.Content.SenderUUID,
				aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
					message.Content.ChatUUID,
					message.Content.SenderUUID,
					partialSessionID,
					chunk,
					[]string{""},
					&map[string]interface{}{
						"thinking_time": thinkingElapsed.Round(time.Millisecond).String(),
						"total_time":    totalTime.Round(time.Millisecond).String(),
						"partial_phase": "responding",
					},
					nil,
					nil,
				),
			)
			return
		}

		aih.botContext.WSHandler.MessageHandler.SendMessage(
			aih.botContext.WSHandler,
			message.Content.SenderUUID,
			aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
				partialSessionID,
				chunk,
				[]string{},
				&map[string]interface{}{
					"total_time":    totalTime.Round(time.Millisecond).String(),
					"partial_phase": "responding",
				},
				nil,
				nil,
			),
		)
	}

	sendThinkingChunk := func(chunk string) {
		if chunk == "" {
			return
		}
		thoughtBuffer.WriteString(chunk)
		currentThoughtStep.WriteString(chunk)
		totalTime := time.Since(startTime)
		thinkingElapsed := currentThinkingDuration()
		aih.botContext.WSHandler.MessageHandler.SendMessage(
			aih.botContext.WSHandler,
			message.Content.SenderUUID,
			aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
				partialSessionID,
				"",
				[]string{chunk},
				&map[string]interface{}{
					"thinking_time": thinkingElapsed.Round(time.Millisecond).String(),
					"total_time":    totalTime.Round(time.Millisecond).String(),
					"partial_phase": "thinking",
				},
				nil,
				nil,
			),
		)
	}

	processChunk := func(chunk string) {
		if !reasoning {
			sendRespondingChunk(chunk)
			return
		}

		currentBuffer.WriteString(chunk)
		bufferStr := currentBuffer.String()

		for {
			if !isThinking {
				startIdx := strings.Index(bufferStr, thinkStartTag)
				if startIdx == -1 {
					keepSuffix := len(thinkStartTag) - 1
					if len(bufferStr) <= keepSuffix {
						break
					}
					sendRespondingChunk(bufferStr[:len(bufferStr)-keepSuffix])
					bufferStr = bufferStr[len(bufferStr)-keepSuffix:]
					break
				}

				if startIdx > 0 {
					sendRespondingChunk(bufferStr[:startIdx])
				}
				isThinking = true
				thinkingStart = time.Now()
				bufferStr = bufferStr[startIdx+len(thinkStartTag):]
				continue
			}

			endIdx := strings.Index(bufferStr, thinkEndTag)
			if endIdx == -1 {
				keepSuffix := len(thinkEndTag) - 1
				if len(bufferStr) <= keepSuffix {
					break
				}
				sendThinkingChunk(bufferStr[:len(bufferStr)-keepSuffix])
				bufferStr = bufferStr[len(bufferStr)-keepSuffix:]
				break
			}

			if endIdx > 0 {
				sendThinkingChunk(bufferStr[:endIdx])
			}
			isThinking = false
			stepDuration := time.Since(thinkingStart)
			thinkingTime += stepDuration
			finalizeThinkingStep(stepDuration)
			thinkingStart = time.Time{}
			bufferStr = bufferStr[endIdx+len(thinkEndTag):]
		}

		currentBuffer.Reset()
		currentBuffer.WriteString(bufferStr)
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("Cancellation received. Stopping response for chat %s\n", message.Content.ChatUUID)
			sendFinalMessage(true, nil)
			return ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
			} else {
				processChunk(chunk)
			}
		case usageInfo, ok := <-usage:
			if !ok {
				usage = nil
			} else {
				tokenUsage = usageInfo
			}
		case toolCall, ok := <-toolCalls:
			if !ok {
				toolCalls = nil
			} else {
				if !hadToolCall {
					hadToolCall = true
					_, extractedThoughts := extractThinkSections(fullText.String())
					appendThoughtEntries(extractedThoughts)
					fullText.Reset()
					currentBuffer.Reset()
				}
				fmt.Println("toolCall", toolCall.ToolName, toolCall.ToolInput)

				// Check if we've already processed this tool call ID
				if _, alreadyProcessed := processedToolCallIds[toolCall.Id]; !alreadyProcessed {
					toolCallRepr := map[string]interface{}{
						"id":        toolCall.Id,
						"name":      toolCall.ToolName,
						"arguments": toolCall.ToolInput,
						"result":    toolCall.Result,
					}

					// Add to our processed IDs map
					processedToolCallIds[toolCall.Id] = true

					// Check if this tool call is already in the list (by ID)
					alreadyInList := false
					for i, registeredToolCall := range allToolCalls {
						if registeredToolCall.(map[string]interface{})["id"] == toolCall.Id {
							alreadyInList = true
							// Update the result if it exists
							allToolCalls[i].(map[string]interface{})["result"] = toolCall.Result
							break
						}
					}

					if tool, found := NewToolByName(toolCall.ToolName); found {
						if tool.GetRequiresConfirmation() {
							toolCallRepr["requires_confirmation"] = true
						}
						if confirmationMeta, ok := parseConfirmActionPayload(toolCall.Result); ok {
							toolCallRepr["requires_confirmation"] = true
							toolCallRepr["confirmation"] = confirmationMeta
						}
					}

					if !alreadyInList {
						allToolCalls = append(allToolCalls, toolCallRepr)
					}

					totalTime := time.Since(startTime)
					partialMeta := map[string]interface{}{
						"total_time":    totalTime.Round(time.Millisecond).String(),
						"tool_call":     toolCall.ToolName,
						"partial_phase": "tool_call",
					}
					confirmableActions := collectConfirmableActions(allToolCalls)
					if len(confirmableActions) > 0 {
						partialMeta["confirmable_actions"] = confirmableActions
					}
					aih.botContext.WSHandler.MessageHandler.SendMessage(
						aih.botContext.WSHandler,
						message.Content.SenderUUID,
						aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
							message.Content.ChatUUID,
							message.Content.SenderUUID,
							partialSessionID,
							"",
							[]string{""},
							&partialMeta,
							&allToolCalls,
							nil,
						),
					)
				}
			}
		case err, ok := <-errs:
			if ok && err != nil {
				log.Printf("streamChatCompletion error: %v", err)
				sendFinalMessage(false, err)
				return fmt.Errorf("%w: %v", ErrResponseAlreadySent, err)
			}
			errs = nil
		}

		if chunks == nil && usage == nil && toolCalls == nil && errs == nil {
			break
		}
	}

	if reasoning {
		remaining := currentBuffer.String()
		if remaining != "" {
			if isThinking {
				sendThinkingChunk(remaining)
			} else {
				sendRespondingChunk(remaining)
			}
			currentBuffer.Reset()
		}
		if isThinking {
			stepDuration := time.Since(thinkingStart)
			thinkingTime += stepDuration
			finalizeThinkingStep(stepDuration)
			isThinking = false
			thinkingStart = time.Time{}
		}
	}

	sendFinalMessage(false, nil)
	return nil
}

// hasSkipCoreTag checks if the tags contain "skip-core"
func (aih *AIHandlerImpl) hasSkipCoreTag(tags []string) bool {
	for _, tag := range tags {
		if tag == "skip-core" {
			return true
		}
	}
	return false
}

// executeToolsOnly executes only the before and after tools without AI completion
func (aih *AIHandlerImpl) executeToolsOnly(ctx context.Context, message wsapi.NewMessage, tools []string, toolInit map[string]interface{}, dynamicTools map[string]interface{}, mcpTools map[string]interface{}) error {
	startTime := time.Now()
	partialSessionID := fmt.Sprintf("%s-skip-core-%d", message.Content.ChatUUID, time.Now().UnixNano())

	// Setup tools
	_, toolMap, interactionStartTools, interactionCompleteTools := aih.setupTools(tools, toolInit, dynamicTools, mcpTools)

	// Add run_callback_function to tools
	tools = append(tools, "run_callback_function")

	// Log the extracted interaction tools for debugging
	if len(interactionStartTools) > 0 {
		log.Printf("Found interaction_start tools: %v", interactionStartTools)
	}
	if len(interactionCompleteTools) > 0 {
		log.Printf("Found interaction_complete tools: %v", interactionCompleteTools)
	}

	// Send start message
	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.StartPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
		),
	)

	// Execute interaction_start tools
	for _, toolName := range interactionStartTools {
		if err := aih.executeTool(ctx, toolName, toolMap, message, partialSessionID); err != nil {
			log.Printf("Error executing interaction_start tool %s: %v", toolName, err)
		}
	}

	// Execute interaction_complete tools
	for _, toolName := range interactionCompleteTools {
		if err := aih.executeTool(ctx, toolName, toolMap, message, partialSessionID); err != nil {
			log.Printf("Error executing interaction_complete tool %s: %v", toolName, err)
		}
	}

	// Send end message
	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.EndPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
		),
	)

	// Send a simple acknowledgment message
	totalTime := time.Since(startTime)
	metadata := map[string]interface{}{
		"total_time": totalTime.Round(time.Millisecond).String(),
		"cancelled":  false,
		"finished":   true,
		"skip_core":  true,
	}

	aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
		Text:     "Tools executed (AI completion skipped due to skip-core tag)",
		MetaData: &metadata,
	})

	return nil
}

// executeTool executes a single tool
func (aih *AIHandlerImpl) executeTool(_ context.Context, toolName string, toolMap map[string]Tool, message wsapi.NewMessage, partialSessionID string) error {
	// Find the tool in the tool map
	tool, exists := toolMap[toolName]
	if !exists {
		return fmt.Errorf("tool %s not found", toolName)
	}

	// Create a simple tool call structure
	toolCall := ToolCall{
		Id:        fmt.Sprintf("skip-core-%d", time.Now().UnixNano()),
		ToolName:  toolName,
		ToolInput: map[string]interface{}{},
		Result:    "",
	}

	// Execute the tool
	result, err := tool.RunTool(toolCall.ToolInput)
	if err != nil {
		return fmt.Errorf("error executing tool %s: %w", toolName, err)
	}

	// Update the tool call with the result
	toolCall.Result = result

	// Send tool execution message
	totalTime := time.Since(time.Now())
	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
			"",
			[]string{""},
			&map[string]interface{}{
				"total_time":    totalTime.Round(time.Millisecond).String(),
				"tool_call":     toolName,
				"skip_core":     true,
				"partial_phase": "tool_call",
			},
			&[]interface{}{map[string]interface{}{
				"id":        toolCall.Id,
				"name":      toolCall.ToolName,
				"arguments": toolCall.ToolInput,
				"result":    toolCall.Result,
			}},
			nil,
		),
	)

	return nil
}

// AIHandlerFactory creates an AI handler with the given context
func AIHandlerFactory(botContext *BotContext) AIHandler {
	return NewAIHandler(botContext)
}
