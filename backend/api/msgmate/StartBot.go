package msgmate

import (
	wsapi "backend/api/websocket"
	"backend/client"
	"backend/database"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"gorm.io/gorm"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

func StartBot(host string, ch *wsapi.WebSocketHandler, username string, password string) error {
	// 'host' e.g.: 'http://localhost:1984'
	// TODO useSSL :=
	hostNoProto := strings.Replace(strings.Replace(host, "http://", "", 1), "https://", "", 1)
	ctx := context.Background() // Persistent context for the WebSocket connection

	// Login the bot
	ocClient := client.NewClient(host)
	err, _ := ocClient.LoginUser(username, password)

	if err != nil {
		return fmt.Errorf("failed to login bot: %w", err)
	}

	err, botUser := ocClient.GetUserInfo()
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}

	chatCaneler := ChatCanceler{
		cancels: make(map[string]context.CancelFunc),
	}

	for {
		// TODO: allow also connecting to the websocket via ssl
		c, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/ws/connect", hostNoProto), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Cookie": []string{fmt.Sprintf("session_id=%s", ocClient.GetSessionId())},
			},
		})
		if err != nil {
			log.Printf("WebSocket connection error: %v", err)
			time.Sleep(5 * time.Second) // Wait before retrying to connect
			continue                    // Retry connecting
		}

		defer c.Close(websocket.StatusNormalClosure, "closing connection") // Ensure connection closed on function termination

		log.Println("Bot connected to WebSocket")

		// Blocking call to continuously read messages
		err = readWebSocketMessages(ocClient, ch, *botUser, ctx, c, &chatCaneler)
		if err != nil {
			log.Printf("Error reading from WebSocket: %v", err)
		}
	}
}

func parseMessage(messageType string, rawMessage json.RawMessage) (error, *wsapi.NewMessage) {
	if messageType == "new_message" {
		var message wsapi.NewMessage
		err := json.Unmarshal(rawMessage, &message)

		if err != nil {
			return err, nil
		}

		return nil, &message
	}

	return fmt.Errorf("Unsupported message type '%s'", messageType), nil
}

type ChatCanceler struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewChatCanceler() *ChatCanceler {
	return &ChatCanceler{
		cancels: make(map[string]context.CancelFunc),
	}
}

func (cc *ChatCanceler) Store(chatUUID string, cancel context.CancelFunc) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.cancels[chatUUID] = cancel
}

func (cc *ChatCanceler) Load(chatUUID string) (context.CancelFunc, bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cf, ok := cc.cancels[chatUUID]
	return cf, ok
}

func (cc *ChatCanceler) Delete(chatUUID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	delete(cc.cancels, chatUUID)
}

func cancelChatResponse(chatCanceler *ChatCanceler, chatUUID string) {
	if cancel, found := chatCanceler.Load(chatUUID); found {
		cancel()
		chatCanceler.Delete(chatUUID)
	}
}

func readWebSocketMessages(
	ocClient *client.Client,
	ch *wsapi.WebSocketHandler,
	botUser database.User,
	ctx context.Context,
	conn *websocket.Conn,
	chatCanceler *ChatCanceler, // pass your ChatCanceler in here
) error {
	// TODO: handle chats in separate goroutines
	for {
		var rawMessage json.RawMessage
		err := wsjson.Read(ctx, conn, &rawMessage)
		if err != nil {
			// Differentiating between normal disconnection and error
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				log.Println("WebSocket closed normally")
				return nil
			}
			return fmt.Errorf("read error: %w", err) // Signal upstream to reconnect
		}

		// Process the message
		err, messageType, chatUUID, senderUUID := preProcessMessage(rawMessage)

		if err != nil {
			log.Printf("Error processing message: %v", err)
			continue // Continue reading messages even if processing one fails
		}

		if senderUUID != botUser.UUID {

			if messageType == "interrupt_signal" {
				log.Printf("Stopping response for chat %s", chatUUID)
				cancelChatResponse(chatCanceler, chatUUID)
				continue
			}

			if _, found := chatCanceler.Load(chatUUID); found {
				// We're already responding to this chat.
				// You can decide what to do: skip, or maybe cancel the old one and start a new one, etc.
				log.Printf("Already responding to chat %s. Skipping or handle logic here.", chatUUID)
				continue
			}

			err, message := parseMessage(messageType, rawMessage)

			if err != nil {
				log.Printf("Error processing message: %v", err)
				continue // Continue reading messages even if processing one fails
			}

			// We may only process this message if there is not yet a context for that chat
			// that way we also avoid responding twich in one chat

			chatCtx, cancel := context.WithCancel(context.Background())

			chatCanceler.Store(chatUUID, cancel)

			go func() {
				defer chatCanceler.Delete(chatUUID)
				if err := respondMsgmate(ocClient, chatCtx, ch, *message); err != nil {
					if err != context.Canceled {
						log.Println("Error while respondMsgmate:", err)
						ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
							Text: "An error occurred while generating the response, please try again later",
						})
					}
				}
			}()
		}
	}
}

func respondMsgmate(ocClient *client.Client, ctx context.Context, ch *wsapi.WebSocketHandler, message wsapi.NewMessage) error {
	startTime := time.Now()
	var thinkingTime time.Duration
	var thinkingStart time.Time

	// 1 - first check if its a command or a plain text message
	if strings.HasPrefix(message.Content.Text, "/") {
		command := strings.Replace(message.Content.Text, "/", "", 1)
		if strings.HasPrefix(command, "pong") {
			ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text: fmt.Sprintf("PONG '%s' ", command),
			})
			return nil
		} else if strings.HasPrefix(command, "loop") {
			var timeSlept float32 = 0.0
			for {
				ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
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
		return fmt.Errorf("Unknown command '%s'", command)
	} else {
		// Load the chat and it's current configuration
		err, chat := ocClient.GetChat(message.Content.ChatUUID)
		if err != nil {
			return err
		}
		// fmt.Println("chat", chat.Config)

		var configMap map[string]interface{}
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

		// Load the past messages
		err, paginatedMessages := ocClient.GetMessages(message.Content.ChatUUID, 1, context)
		if err != nil {
			return err
		}
		// fmt.Println("paginatedMessages", paginatedMessages)
		openAiMessages := []map[string]interface{}{}
		currentMessageIncluded := false

		for i := len(paginatedMessages.Rows) - 1; i >= 0; i-- {
			msg := paginatedMessages.Rows[i]
			if msg.SenderUUID == ocClient.User.UUID {
				openAiMessages = append(openAiMessages, map[string]interface{}{"role": "assistant", "content": msg.Text})
				// also check for possible past tool calls
			} else {
				openAiMessages = append(openAiMessages, map[string]interface{}{"role": "user", "content": msg.Text})
			}
			if msg.Text == message.Content.Text {
				currentMessageIncluded = true
			}
		}

		if !currentMessageIncluded {
			openAiMessages = append(openAiMessages, map[string]interface{}{"role": "user", "content": message.Content.Text})
		}

		fmt.Println("TOOOLS openAiMessages", tools)

		var toolsData []interface{}
		toolMap := map[string]Tool{}
		if len(tools) > 0 {
			for _, tool := range AllTools {
				if slices.Contains(tools, tool.GetToolName()) {
					toolsData = append(toolsData, tool.ConstructTool())
					toolMap[tool.GetToolName()] = tool
				}
			}
		}

		chunks, usage, toolCalls, errs := streamChatCompletion(
			endpoint,
			model,
			backend,
			openAiMessages,
			toolsData,
			toolMap,
			ocClient.GetApiKey(backend),
		)

		var allToolCalls []interface{}
		var fullText, thoughtText, thoughtBuffer strings.Builder
		var isThinking bool
		var currentBuffer strings.Builder
		var tokenUsage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}

		ch.MessageHandler.SendMessage(
			ch,
			message.Content.SenderUUID,
			ch.MessageHandler.StartPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
			),
		)

		// Helper function to send final message and cleanup
		sendFinalMessage := func(isCancelled bool) {
			// If we're still thinking when finishing, add the final thinking time
			totalTime := time.Since(startTime)

			ch.MessageHandler.SendMessage(
				ch,
				message.Content.SenderUUID,
				ch.MessageHandler.EndPartialMessage(
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
			}
			if tokenUsage != nil {
				metadata["token_usage"] = tokenUsage
			}
			if reasoning {
				metadata["thinking_time"] = thinkingTime.Round(time.Millisecond).String()
			}

			if reasoning {
				ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
					Text:      text,
					Reasoning: []string{thoughtText.String()},
					MetaData:  &metadata,
					ToolCalls: &allToolCalls,
				})
			} else {
				ocClient.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
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
						ch.MessageHandler.SendMessage(
							ch,
							message.Content.SenderUUID,
							ch.MessageHandler.NewPartialMessage(
								message.Content.ChatUUID,
								message.Content.SenderUUID,
								chunk,
								[]string{""},
								&map[string]interface{}{
									"thinking_time": thinkingTime.Round(time.Millisecond).String(),
									"total_time":    totalTime.Round(time.Millisecond).String(),
								},
								nil,
							),
						)
					} else {
						thinkingTime = time.Since(thinkingStart)
						thoughtBuffer.WriteString(chunk)
						ch.MessageHandler.SendMessage(
							ch,
							message.Content.SenderUUID,
							ch.MessageHandler.NewPartialMessage(
								message.Content.ChatUUID,
								message.Content.SenderUUID,
								"",
								[]string{chunk},
								&map[string]interface{}{
									"thinking_time": thinkingTime.Round(time.Millisecond).String(),
									"total_time":    totalTime.Round(time.Millisecond).String(),
								},
								nil,
							),
						)
					}
				}
			} else {
				fullText.WriteString(chunk)
				totalTime := time.Since(startTime)
				ch.MessageHandler.SendMessage(
					ch,
					message.Content.SenderUUID,
					ch.MessageHandler.NewPartialMessage(
						message.Content.ChatUUID,
						message.Content.SenderUUID,
						chunk,
						[]string{},
						&map[string]interface{}{
							"total_time": totalTime.Round(time.Millisecond).String(),
						},
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
					toolCallRepr := map[string]interface{}{
						"id":        toolCall.Id,
						"name":      toolCall.ToolName,
						"arguments": toolCall.ToolInput,
						"result":    toolCall.Result,
					}
					// first check if the tool call is already in the list
					alreadyInList := false
					for _, registeredToolCall := range allToolCalls {
						if registeredToolCall.(map[string]interface{})["id"] == toolCall.Id {
							alreadyInList = true
							// then update the result
							registeredToolCall.(map[string]interface{})["result"] = toolCall.Result
							break
						}
					}
					if !alreadyInList {
						allToolCalls = append(allToolCalls, toolCallRepr)
					}

					totalTime := time.Since(startTime)
					ch.MessageHandler.SendMessage(
						ch,
						message.Content.SenderUUID,
						ch.MessageHandler.NewPartialMessage(
							message.Content.ChatUUID,
							message.Content.SenderUUID,
							"",
							[]string{""},
							&map[string]interface{}{
								"thinking_time": thinkingTime.Round(time.Millisecond).String(),
								"total_time":    totalTime.Round(time.Millisecond).String(),
							},
							&allToolCalls,
						),
					)
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
}

func preProcessMessage(rawMessage json.RawMessage) (error, string, string, string) {
	var chatMessageTypes = []string{"new_message", "interrupt_signal"}
	var messageMap map[string]interface{}
	err := json.Unmarshal(rawMessage, &messageMap)
	if err != nil {
		return err, "", "", ""
	}

	messageType := messageMap["type"].(string)

	if slices.Contains(chatMessageTypes, messageType) {
		chatUUID := (messageMap["content"].(map[string]interface{}))["chat_uuid"].(string)
		senderUUID := (messageMap["content"].(map[string]interface{}))["sender_uuid"].(string)
		return nil, messageType, chatUUID, senderUUID
	}

	return fmt.Errorf("Cannot process category"), "", "", ""

}

func mapGetOrDefault[T any](m map[string]interface{}, key string, defaultValue T) T {
	if m == nil {
		return defaultValue
	}

	if val, exists := m[key]; exists {
		// Try direct type conversion first
		if converted, ok := val.(T); ok {
			return converted
		}
		// Special handling for slices/arrays
		switch any(defaultValue).(type) {
		case []string:
			if slice, ok := val.([]interface{}); ok {
				result := make([]string, len(slice))
				for i, item := range slice {
					if str, ok := item.(string); ok {
						result[i] = str
					}
				}
				return any(result).(T)
			}
		}
	}

	return defaultValue
}

func CreateOrUpdateBotProfile(DB *gorm.DB, botUser database.User) error {
	// first check if the profile exists
	var botProfile database.PublicProfile
	DB.Where("user_id = ?", botUser.ID).First(&botProfile)
	if botProfile.ID != 0 {
		// Delete the old profile
		DB.Delete(&botProfile)
		// and overwrite it with the new one
	}

	botProfileInfo := map[string]interface{}{
		"name":        "Bot",
		"description": "This is a bot user",
		"models": []interface{}{
			map[string]interface{}{
				"title":       "gpt-4o",
				"description": "OpenAI's GPT-4o, optimized for specific applications with advanced tool and function support.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"tools":         []string{"get_weather", "get_current_time"},
					"model":         "gpt-4o",
					"endpoint":      "https://api.openai.com/v1/",
					"backend":       "openai",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "o3-mini-2025-01-31",
				"description": "OpenAI's O3 Mini, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"model":         "o3-mini-2025-01-31",
					"endpoint":      "https://api.openai.com/v1/",
					"backend":       "openai",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "deepseek-ai/DeepSeek-V3",
				"description": "DeepSeek's DeepSeek V3, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"model":         "deepseek-ai/DeepSeek-V3",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "meta-llama/Llama-3.3-70B-Instruct-Turbo",
				"description": "Meta's Llama 3.3, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"model":         "meta-llama/Llama-3.3-70B-Instruct-Turbo",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "deepseek-ai/DeepSeek-V3",
				"description": "DeepSeek's DeepSeek V3, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"model":         "deepseek-ai/DeepSeek-V3",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "deepseek-ai/DeepSeek-R1",
				"description": "DeepSeek's DeepSeek Coder, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"reasoning":     true,
					"model":         "deepseek-ai/DeepSeek-R1",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "meta-llama/Meta-Llama-3.1-405B-Instruct",
				"description": "Meta's Llama 3.1, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"reasoning":     false,
					"tools":         []string{"get_current_time", "get_weather"},
					"model":         "meta-llama/Meta-Llama-3.1-405B-Instruct",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
		},
	}
	// create default public profile for bot user
	botProfileBytes, err := json.Marshal(botProfileInfo)
	if err != nil {
		return err
	}
	botProfile = database.PublicProfile{
		ProfileData: botProfileBytes,
		UserId:      botUser.ID,
	}
	q := DB.Create(&botProfile)
	if q.Error != nil {
		return q.Error
	}
	return nil
}

func performToolProcessing(
	ocClient *client.Client,
	endpoint string,
	model string,
	backend string,
	openAiMessages []map[string]string,
	tools []string,
	apiKey string,
) ([]map[string]string, error) {
	// first we have to retrieve the tools
	var toolsInterface []Tool
	toolsInterface = append(toolsInterface, NewWeatherTool()) // TODO: load tools dynamically based on tool name
	var toolData []interface{}
	for _, tool := range toolsInterface {
		toolData = append(toolData, tool.ConstructTool())
	}
	fmt.Println("toolData", toolData)
	toolCallsChan, usageChan, errChan := toolRequest(endpoint, model, backend, openAiMessages, toolData, apiKey)

	// Process all channels
	for {
		select {
		case toolCallsResult, ok := <-toolCallsChan:
			if !ok {
				toolCallsChan = nil
			} else {
				// Process tool calls and append results to messages
				fmt.Println("toolCallsResult", toolCallsResult)
				for _, toolCall := range toolCallsResult.ToolCalls {
					// Find the corresponding tool
					var tool Tool
					for _, t := range tools {
						if t == toolCall.ToolName {
							tool = NewWeatherTool() // TODO: Make this dynamic based on tool name
							break
						}
					}

					if tool != nil {
						// Execute the tool
						result, err := tool.RunTool(toolCall.ToolInput)
						if err != nil {
							return nil, fmt.Errorf("error executing tool %s: %w", toolCall.ToolName, err)
						}

						// Append the tool call and result to messages
						openAiMessages = append(openAiMessages,
							map[string]string{"role": "assistant", "content": fmt.Sprintf("I'll use the %s to help answer your question.", toolCall.ToolName)},
							map[string]string{"role": "function", "name": toolCall.ToolName, "content": result},
						)
						fmt.Println("openAiMessages", openAiMessages)
					}
				}
			}
		case _, ok := <-usageChan:
			if !ok {
				fmt.Println("usageChan", usageChan)
				usageChan = nil
			}
		case err, ok := <-errChan:
			if ok && err != nil {
				return nil, fmt.Errorf("tool processing error: %w", err)
			}
			errChan = nil
		}

		if toolCallsChan == nil && usageChan == nil && errChan == nil {
			break
		}
	}

	return openAiMessages, nil
}
