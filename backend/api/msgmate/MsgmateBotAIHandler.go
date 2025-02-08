package msgmate

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	wsapi "backend/api/websocket"
	"backend/client"
)

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
	systemPrompt := mapGetOrDefault[string](configMap, "system_prompt", "You are a helpful assistant.")

	// Load the past messages
	err, paginatedMessages := aih.botContext.Client.GetMessages(message.Content.ChatUUID, 1, context)
	if err != nil {
		return err
	}

	// Build OpenAI messages
	openAiMessages := aih.buildOpenAIMessages(&paginatedMessages, message, systemPrompt, backend)

	// Setup tools
	toolsData, toolMap, interactionStartTools, interactionCompleteTools := aih.setupTools(tools, toolInit)

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
			openAiMessages = append(openAiMessages, map[string]interface{}{"role": "assistant", "content": msg.Text})
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
		contentArray := aih.processCurrentMessageAttachments(message.Content.Text, attachments, backend)
		openAiMessages = append(openAiMessages, map[string]interface{}{
			"role":    "user",
			"content": contentArray,
		})
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
func (aih *AIHandlerImpl) setupTools(tools []string, toolInit map[string]interface{}) ([]interface{}, map[string]Tool, []string, []string) {
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
			// Skip tools that contain ':' as they are default bot tools
			actualToolName := toolName
			if strings.Contains(toolName, ":") {
				// Extract special interaction tools
				if strings.HasPrefix(toolName, "interaction_start:") {
					interactionStartTools = append(interactionStartTools, toolName)
				} else if strings.HasPrefix(toolName, "interaction_complete:") {
					interactionCompleteTools = append(interactionCompleteTools, toolName)
				}
				// Extract everything after the colon
				parts := strings.SplitN(toolName, ":", 2)
				if len(parts) == 2 {
					actualToolName = parts[1]
				}
			}
			log.Printf("====> Processing tool registration for %s, with actual tool name %s", toolName, actualToolName)

			// Find the tool in AllTools
			toolFound := false
			for _, tool := range AllTools {
				if tool.GetToolName() == actualToolName {
					toolFound = true
					log.Printf("====> Found tool %s in AllTools (RequiresInit: %v)", actualToolName, tool.GetRequiresInit())
					if tool.GetToolName() == "run_callback_function" {
						tool = NewRunCallbackFunctionTool()
					}
					toolsData = append(toolsData, tool.ConstructTool())
					toolMap[toolName] = tool
					if tool.GetRequiresInit() {
						if _, ok := toolInit[toolName]; ok {
							log.Printf("Setting init data for tool %s", tool.GetToolName())
							tool.SetInitData(toolInit[toolName])
							log.Printf("Init data set for tool %s", tool.GetToolName())
						} else {
							log.Printf("Tool init data not found for tool %s", tool.GetToolName())
							tool.SetInitData(map[string]interface{}{})
						}
					}
					break
				}
			}
			if !toolFound {
				log.Printf("====> WARNING: Tool %s NOT FOUND in AllTools!", actualToolName)
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
	var fullText, thoughtText, thoughtBuffer strings.Builder
	var isThinking bool
	var currentBuffer strings.Builder
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
		),
	)

	// Helper function to send final message and cleanup
	sendFinalMessage := func(isCancelled bool) {
		// If we're still thinking when finishing, add the final thinking time
		totalTime := time.Since(startTime)

		aih.botContext.WSHandler.MessageHandler.SendMessage(
			aih.botContext.WSHandler,
			message.Content.SenderUUID,
			aih.botContext.WSHandler.MessageHandler.EndPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
			),
		)

		text := fullText.String()
		if isCancelled {
			text += "\n[cancelled]"
			if reasoning {
				thoughtText.WriteString(thoughtBuffer.String())
				thoughtText.WriteString("\n[cancelled]")
			}
		}

		metadata := map[string]interface{}{
			"total_time": totalTime.Round(time.Millisecond).String(),
			"cancelled":  isCancelled,
			"finished":   true,
		}
		if tokenUsage != nil {
			metadata["token_usage"] = tokenUsage
		}
		if reasoning {
			metadata["thinking_time"] = thinkingTime.Round(time.Millisecond).String()
		}

		if reasoning {
			aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text:      text,
				Reasoning: []string{thoughtText.String()},
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

	processChunk := func(chunk string) {
		if reasoning {
			currentBuffer.WriteString(chunk)
			bufferStr := currentBuffer.String()

			// Check for thinking tags
			if strings.Contains(bufferStr, "<think>") && !isThinking {
				isThinking = true
				thinkingStart = time.Now()
				currentBuffer.Reset()
			} else if strings.Contains(bufferStr, "</think>") && isThinking {
				isThinking = false
				thinkingTime = time.Since(thinkingStart)
				// Extract thought content (everything before </think>)
				thought := bufferStr[:strings.Index(bufferStr, "</think>")]
				thoughtText.WriteString(thought)
				currentBuffer.Reset()
			} else {
				totalTime := time.Since(startTime)
				if !isThinking {
					fullText.WriteString(chunk)
					aih.botContext.WSHandler.MessageHandler.SendMessage(
						aih.botContext.WSHandler,
						message.Content.SenderUUID,
						aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
							message.Content.ChatUUID,
							message.Content.SenderUUID,
							chunk,
							[]string{""},
							&map[string]interface{}{
								"thinking_time": thinkingTime.Round(time.Millisecond).String(),
								"total_time":    totalTime.Round(time.Millisecond).String(),
							},
							nil,
							nil,
						),
					)
				} else {
					thinkingTime = time.Since(thinkingStart)
					thoughtBuffer.WriteString(chunk)
					aih.botContext.WSHandler.MessageHandler.SendMessage(
						aih.botContext.WSHandler,
						message.Content.SenderUUID,
						aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
							message.Content.ChatUUID,
							message.Content.SenderUUID,
							"",
							[]string{chunk},
							&map[string]interface{}{
								"thinking_time": thinkingTime.Round(time.Millisecond).String(),
								"total_time":    totalTime.Round(time.Millisecond).String(),
							},
							nil,
							nil,
						),
					)
				}
			}
		} else {
			fullText.WriteString(chunk)
			totalTime := time.Since(startTime)
			aih.botContext.WSHandler.MessageHandler.SendMessage(
				aih.botContext.WSHandler,
				message.Content.SenderUUID,
				aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
					message.Content.ChatUUID,
					message.Content.SenderUUID,
					chunk,
					[]string{},
					&map[string]interface{}{
						"total_time": totalTime.Round(time.Millisecond).String(),
					},
					nil,
					nil,
				),
			)
		}
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("Cancellation received. Stopping response for chat %s\n", message.Content.ChatUUID)
			sendFinalMessage(true)
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

					if !alreadyInList {
						allToolCalls = append(allToolCalls, toolCallRepr)
					}

					totalTime := time.Since(startTime)
					aih.botContext.WSHandler.MessageHandler.SendMessage(
						aih.botContext.WSHandler,
						message.Content.SenderUUID,
						aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
							message.Content.ChatUUID,
							message.Content.SenderUUID,
							"",
							[]string{""},
							&map[string]interface{}{
								"total_time": totalTime.Round(time.Millisecond).String(),
								"tool_call":  toolCall.ToolName,
							},
							&allToolCalls,
							nil,
						),
					)
				}
			}
		case err, ok := <-errs:
			if ok && err != nil {
				log.Printf("streamChatCompletion error: %v", err)
				sendFinalMessage(true)
				return err
			}
			errs = nil
		}

		if chunks == nil && usage == nil && toolCalls == nil && errs == nil {
			break
		}
	}

	sendFinalMessage(false)
	return nil
}

// AIHandlerFactory creates an AI handler with the given context
func AIHandlerFactory(botContext *BotContext) AIHandler {
	return NewAIHandler(botContext)
}
