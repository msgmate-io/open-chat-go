package msgmate

import (
	wsapi "backend/api/websocket"
	"backend/client"
	"backend/database"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"gorm.io/gorm"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// StartBotWithRestart starts the bot with automatic restart capability and error logging
func StartBotWithRestart(host string, ch *wsapi.WebSocketHandler, username string, password string) {
	StartBotWithRestartContext(context.Background(), host, ch, username, password)
}

// StartBotWithRestartContext starts the bot with automatic restart capability, error logging, and context cancellation
func StartBotWithRestartContext(ctx context.Context, host string, ch *wsapi.WebSocketHandler, username string, password string) {
	go func() {
		restartCount := 0
		maxRestartDelay := 30 * time.Second
		baseRestartDelay := 5 * time.Second
		maxRestartAttempts := 1000 // Prevent infinite restarts in case of persistent issues

		defer func() {
			if r := recover(); r != nil {
				log.Printf("Bot restart loop panicked: %v", r)
				logErrorToDisk(fmt.Errorf("panic: %v", r), restartCount, username)
			}
		}()

		for restartCount < maxRestartAttempts {
			select {
			case <-ctx.Done():
				log.Printf("Bot restart loop cancelled: %v", ctx.Err())
				return
			default:
				// Continue with bot restart logic
			}

			restartCount++
			log.Printf("Starting bot (attempt %d)...", restartCount)

			// Start the bot and capture any errors
			err := StartBot(host, ch, username, password)

			// Log the error to disk
			if err != nil {
				logErrorToDisk(err, restartCount, username)
				log.Printf("Bot crashed (attempt %d): %v", restartCount, err)
			} else {
				log.Printf("Bot stopped normally (attempt %d)", restartCount)
				// If the bot stopped normally (no error), we might want to exit the restart loop
				// For now, we'll continue restarting to handle cases where the bot exits gracefully
				// but we want it to keep running
			}

			// Calculate restart delay with exponential backoff (capped at maxRestartDelay)
			restartDelay := time.Duration(restartCount) * baseRestartDelay
			if restartDelay > maxRestartDelay {
				restartDelay = maxRestartDelay
			}

			log.Printf("Restarting bot in %v (attempt %d)", restartDelay, restartCount+1)

			// Use a timer with context cancellation for the restart delay
			timer := time.NewTimer(restartDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				log.Printf("Bot restart loop cancelled during delay: %v", ctx.Err())
				return
			case <-timer.C:
				// Continue to next iteration
			}
		}

		log.Printf("Bot restart loop stopped after %d attempts (max reached)", maxRestartAttempts)
		logErrorToDisk(fmt.Errorf("max restart attempts reached (%d)", maxRestartAttempts), restartCount, username)
	}()
}

// logErrorToDisk writes bot errors to a log file
func logErrorToDisk(err error, attempt int, username string) {
	// Create logs directory if it doesn't exist
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Printf("Failed to create logs directory: %v", err)
		return
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logFileName := filepath.Join(logsDir, fmt.Sprintf("bot_errors_%s.log", timestamp))

	// Open log file in append mode
	logFile, err := os.OpenFile(logFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		return
	}
	defer logFile.Close()

	// Get current time for logging
	now := time.Now()

	// Write detailed error entry
	errorEntry := fmt.Sprintf("[%s] Bot crash (attempt %d) for user '%s':\n",
		now.Format("2006-01-02 15:04:05"),
		attempt,
		username)

	errorEntry += fmt.Sprintf("  Error: %v\n", err)
	errorEntry += fmt.Sprintf("  Timestamp: %s\n", now.Format(time.RFC3339))
	errorEntry += fmt.Sprintf("  Attempt: %d\n", attempt)
	errorEntry += fmt.Sprintf("  User: %s\n", username)

	// Add separator for readability
	errorEntry += "  " + strings.Repeat("-", 50) + "\n"

	if _, writeErr := logFile.WriteString(errorEntry); writeErr != nil {
		log.Printf("Failed to write to log file: %v", writeErr)
	}
}

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

	// --- SESSION REFRESH LOGIC ---
	refreshInterval := 12 * time.Hour        // Refresh session every 12 hours (should be less than session expiry)
	sessionRefresh := make(chan struct{}, 1) // signal channel for session refresh
	var sessionMu sync.Mutex                 // mutex to protect session id updates

	go func() {
		for {
			time.Sleep(refreshInterval)
			log.Println("Refreshing bot session...")
			sessionMu.Lock()
			err, _ := ocClient.LoginUser(username, password)
			sessionMu.Unlock()
			if err != nil {
				log.Printf("Failed to refresh session: %v", err)
				continue
			}
			// Signal main loop to reconnect WebSocket
			select {
			case sessionRefresh <- struct{}{}:
				log.Println("Session refresh signal sent.")
			default:
				// If signal already pending, skip
			}
		}
	}()
	// --- END SESSION REFRESH LOGIC ---

	for {
		// TODO: allow also connecting to the websocket via ssl
		sessionMu.Lock()
		wsSessionId := ocClient.GetSessionId()
		sessionMu.Unlock()
		c, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/ws/connect", hostNoProto), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Cookie": []string{fmt.Sprintf("session_id=%s", wsSessionId)},
			},
		})
		if err != nil {
			log.Printf("WebSocket connection error: %v", err)
			time.Sleep(5 * time.Second) // Wait before retrying to connect
			continue                    // Retry connecting
		}

		// Use a channel to close the connection on session refresh
		closeConn := make(chan struct{})
		var closeOnce sync.Once
		// Watch for session refresh
		go func() {
			<-sessionRefresh
			closeOnce.Do(func() {
				log.Println("Closing WebSocket due to session refresh...")
				c.Close(websocket.StatusNormalClosure, "session refresh")
				close(closeConn)
			})
		}()

		log.Println("Bot connected to WebSocket")

		// Blocking call to continuously read messages
		err = readWebSocketMessages(ocClient, ch, *botUser, ctx, c, &chatCaneler)
		if err != nil {
			log.Printf("Error reading from WebSocket: %v", err)
		}
		// Wait for closeConn if session refresh triggered, otherwise just loop
		select {
		case <-closeConn:
			log.Println("WebSocket closed for session refresh, reconnecting...")
			continue
		default:
			// Normal error, reconnect after delay
			time.Sleep(5 * time.Second)
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
		toolInit := mapGetOrDefault[map[string]interface{}](configMap, "tool_init", map[string]interface{}{})
		systemPrompt := mapGetOrDefault[string](configMap, "system_prompt", "You are a helpful assistant.")

		// Load the past messages
		err, paginatedMessages := ocClient.GetMessages(message.Content.ChatUUID, 1, context)
		if err != nil {
			return err
		}
		// fmt.Println("paginatedMessages", paginatedMessages)
		openAiMessages := []map[string]interface{}{}
		currentMessageIncluded := false

		openAiMessages = append(openAiMessages, map[string]interface{}{"role": "system", "content": systemPrompt})

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

			if msg.SenderUUID == ocClient.User.UUID {
				openAiMessages = append(openAiMessages, map[string]interface{}{"role": "assistant", "content": msg.Text})
				// also check for possible past tool calls
			} else {
				// Handle user messages with potential attachments
				if len(attachments) > 0 {
					// Create content array with text and file references
					contentArray := []map[string]interface{}{}

					// Add text content if it exists
					if msg.Text != "" {
						contentArray = append(contentArray, map[string]interface{}{
							"type": "text",
							"text": msg.Text,
						})
					}

					// Add each file attachment
					for _, att := range attachments {
						if attMap, ok := att.(map[string]interface{}); ok {
							if fileID, ok := attMap["file_id"].(string); ok {
								mimeType, _ := attMap["mime_type"].(string)

								// Check if this is an image
								if mimeType != "" && strings.HasPrefix(mimeType, "image/") {
									// For images, convert to base64 and use vision format
									base64Data, contentType, err := retrieveFileData(ocClient, fileID)
									if err != nil {
										log.Printf("Error retrieving image data for %s: %v", fileID, err)
										continue
									}

									contentArray = append(contentArray, map[string]interface{}{
										"type": "image_url",
										"image_url": map[string]interface{}{
											"url": fmt.Sprintf("data:%s;base64,%s", contentType, base64Data),
										},
									})
								} else {
									// For non-images, handle based on backend
									if backend == "openai" {
										// For OpenAI backend, use file ID approach
										openAIFileID, err := getOpenAIFileID(ocClient, fileID)
										if err != nil {
											log.Printf("Error getting OpenAI file ID for %s: %v", fileID, err)
											continue
										}

										if openAIFileID == "" {
											// Upload file to OpenAI if not already uploaded
											openAIFileID, err = uploadFileToOpenAI(ocClient, fileID, mimeType)
											if err != nil {
												log.Printf("Error uploading file to OpenAI for %s: %v", fileID, err)
												continue
											}
										}

										// Add file reference to content array
										contentArray = append(contentArray, map[string]interface{}{
											"type": "file",
											"file": map[string]interface{}{
												"file_id": openAIFileID,
											},
										})
									} else {
										// For non-OpenAI backends, skip file attachments for now
										log.Printf("File attachments not supported for backend %s, skipping file %s", backend, fileID)
									}
								}
							}
						}
					}

					// Add the message with content array
					openAiMessages = append(openAiMessages, map[string]interface{}{
						"role":    "user",
						"content": contentArray,
					})
				} else {
					openAiMessages = append(openAiMessages, map[string]interface{}{"role": "user", "content": msg.Text})
				}
			}
			if msg.Text == message.Content.Text {
				currentMessageIncluded = true
			}
		}

		if !currentMessageIncluded {
			// Handle current message with potential attachments
			if message.Content.Attachments != nil && len(*message.Content.Attachments) > 0 {
				// Create content array with text and file references
				contentArray := []map[string]interface{}{}

				// Add text content if it exists
				if message.Content.Text != "" {
					contentArray = append(contentArray, map[string]interface{}{
						"type": "text",
						"text": message.Content.Text,
					})
				}

				// Add each file attachment
				for _, att := range *message.Content.Attachments {
					// Check if this is an image
					if att.MimeType != "" && strings.HasPrefix(att.MimeType, "image/") {
						// For images, convert to base64 and use vision format
						base64Data, contentType, err := retrieveFileData(ocClient, att.FileID)
						if err != nil {
							log.Printf("Error retrieving image data for %s: %v", att.FileID, err)
							continue
						}

						contentArray = append(contentArray, map[string]interface{}{
							"type": "image_url",
							"image_url": map[string]interface{}{
								"url": fmt.Sprintf("data:%s;base64,%s", contentType, base64Data),
							},
						})
					} else {
						// For non-images, handle based on backend
						if backend == "openai" {
							// For OpenAI backend, use file ID approach
							openAIFileID, err := getOpenAIFileID(ocClient, att.FileID)
							if err != nil {
								log.Printf("Error getting OpenAI file ID for %s: %v", att.FileID, err)
								continue
							}

							if openAIFileID == "" {
								// Upload file to OpenAI if not already uploaded
								openAIFileID, err = uploadFileToOpenAI(ocClient, att.FileID, att.MimeType)
								if err != nil {
									log.Printf("Error uploading file to OpenAI for %s: %v", att.FileID, err)
									continue
								}
							}

							// Add file reference to content array
							contentArray = append(contentArray, map[string]interface{}{
								"type": "file",
								"file": map[string]interface{}{
									"file_id": openAIFileID,
								},
							})
						} else {
							// For non-OpenAI backends, skip file attachments for now
							log.Printf("File attachments not supported for backend %s, skipping file %s", backend, att.FileID)
						}
					}
				}

				// Add the message with content array
				openAiMessages = append(openAiMessages, map[string]interface{}{
					"role":    "user",
					"content": contentArray,
				})
			} else {
				openAiMessages = append(openAiMessages, map[string]interface{}{"role": "user", "content": message.Content.Text})
			}
		}

		fmt.Println("TOOOLS openAiMessages", tools)

		// Pretty print the openAiMessages for debugging
		prettyMessages, _ := json.MarshalIndent(openAiMessages, "", "  ")
		fmt.Println("=== OPENAI MESSAGES ===")
		fmt.Println(string(prettyMessages))
		fmt.Println("=== END OPENAI MESSAGES ===")

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

		tools = append(tools, "run_callback_function")

		// Log the extracted interaction tools for debugging
		if len(interactionStartTools) > 0 {
			log.Printf("Found interaction_start tools: %v", interactionStartTools)
		}
		if len(interactionCompleteTools) > 0 {
			log.Printf("Found interaction_complete tools: %v", interactionCompleteTools)
		}

		chunks, usage, toolCalls, errs := streamChatCompletion(
			endpoint,
			model,
			backend,
			openAiMessages,
			toolsData,
			toolMap,
			ocClient.GetApiKey(backend),
			interactionStartTools,
			interactionCompleteTools,
			GetGlobalMsgmateHandler(),
		)

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
				"finished":   true,
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
						ch.MessageHandler.SendMessage(
							ch,
							message.Content.SenderUUID,
							ch.MessageHandler.NewPartialMessage(
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
		// Hard delete the old profile
		DB.Unscoped().Delete(&botProfile)
	}

	// Create profile data and new profile instance
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
				"title":       "meta-llama/Llama-3.3-70B-Instruct-Turbo",
				"description": "Meta's Llama 3.3, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"model":         "meta-llama/Llama-3.3-70B-Instruct-Turbo",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"tools":         []string{"get_current_time", "get_weather", "get_random_number"},
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
				"description": "Meta's Llama 3.1, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"model":         "meta-llama/Llama-3.3-70B-Instruct-Turbo",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"tools":         []string{"get_current_time", "get_weather", "get_random_number"},
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
					"tools":         []string{"get_current_time", "get_weather", "get_random_number"},
					"model":         "meta-llama/Meta-Llama-3.1-405B-Instruct",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
			map[string]interface{}{
				"title":       "meta-llama/Meta-Llama-3.1-8B-Instruct",
				"description": "Meta's Llama 3.1, a powerful and efficient language model.",
				"configuration": map[string]interface{}{
					"temperature":   0.7,
					"max_tokens":    4096,
					"reasoning":     false,
					"tools":         []string{"get_current_time", "get_weather", "get_random_number"},
					"model":         "meta-llama/Meta-Llama-3.1-8B-Instruct",
					"endpoint":      "https://api.deepinfra.com/v1/openai",
					"backend":       "deepinfra",
					"context":       10,
					"system_prompt": "You are a helpful assistant.",
				},
			},
		},
	}

	botProfileBytes, err := json.Marshal(botProfileInfo)
	if err != nil {
		return err
	}

	// Create a new profile instance
	newBotProfile := database.PublicProfile{
		ProfileData: botProfileBytes,
		UserId:      botUser.ID,
	}

	q := DB.Create(&newBotProfile)
	if q.Error != nil {
		return q.Error
	}

	return nil
}

// retrieveFileData fetches a file by fileID and converts it to base64
func retrieveFileData(ocClient *client.Client, fileID string) (string, string, error) {
	// Create request to download the file
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s", ocClient.GetHost(), fileID), nil)
	if err != nil {
		return "", "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", ocClient.GetSessionId()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("error downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("file download failed with status: %d", resp.StatusCode)
	}

	// Read the file content
	fileData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("error reading file data: %w", err)
	}

	// Convert to base64
	base64Data := base64.StdEncoding.EncodeToString(fileData)

	// Get content type from response headers
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return base64Data, contentType, nil
}

// getOpenAIFileID retrieves the OpenAI file ID for a given file ID
func getOpenAIFileID(ocClient *client.Client, fileID string) (string, error) {
	// Create request to get file info
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/info", ocClient.GetHost(), fileID), nil)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", ocClient.GetSessionId()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error getting file info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("file info request failed with status: %d", resp.StatusCode)
	}

	// Parse the response to get file metadata
	var fileInfo struct {
		FileID       string                 `json:"file_id"`
		FileName     string                 `json:"file_name"`
		Size         int64                  `json:"size"`
		MimeType     string                 `json:"mime_type"`
		UploadedAt   string                 `json:"uploaded_at"`
		OpenAIFileID string                 `json:"openai_file_id,omitempty"`
		MetaData     map[string]interface{} `json:"meta_data,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return "", fmt.Errorf("error decoding file info: %w", err)
	}

	// Check if OpenAI file ID is in the response
	if fileInfo.OpenAIFileID != "" {
		return fileInfo.OpenAIFileID, nil
	}

	// Check if OpenAI file ID is in metadata
	if fileInfo.MetaData != nil {
		if openAIFileID, ok := fileInfo.MetaData["openai_file_id"].(string); ok && openAIFileID != "" {
			return openAIFileID, nil
		}
	}

	return "", nil
}

// uploadFileToOpenAI uploads a file to OpenAI's files API
func uploadFileToOpenAI(ocClient *client.Client, fileID string, mimeType string) (string, error) {
	// Get OpenAI API key
	openAIKey := ocClient.GetApiKey("openai")
	if openAIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	// First, get the file data from our server
	fileData, _, err := retrieveFileData(ocClient, fileID)
	if err != nil {
		return "", fmt.Errorf("error retrieving file data: %w", err)
	}

	// Decode base64 data back to bytes
	fileBytes, err := base64.StdEncoding.DecodeString(fileData)
	if err != nil {
		return "", fmt.Errorf("error decoding base64 data: %w", err)
	}

	// Get file info to get the filename
	fileInfo, err := getFileInfo(ocClient, fileID)
	if err != nil {
		return "", fmt.Errorf("error getting file info: %w", err)
	}

	// Create multipart form data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add the file
	part, err := writer.CreateFormFile("file", fileInfo.FileName)
	if err != nil {
		return "", fmt.Errorf("error creating form file: %w", err)
	}

	_, err = io.Copy(part, bytes.NewReader(fileBytes))
	if err != nil {
		return "", fmt.Errorf("error copying file data: %w", err)
	}

	// Add purpose field
	err = writer.WriteField("purpose", "assistants")
	if err != nil {
		return "", fmt.Errorf("error writing purpose field: %w", err)
	}

	writer.Close()

	// Create request to OpenAI
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/files", &buf)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+openAIKey)

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var openAIResp struct {
		ID        string `json:"id"`
		Object    string `json:"object"`
		Bytes     int    `json:"bytes"`
		CreatedAt int64  `json:"created_at"`
		Filename  string `json:"filename"`
		Purpose   string `json:"purpose"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	// Note: We don't update the file metadata here since there's no metadata update endpoint
	// The OpenAI file ID will be retrieved again next time the file is processed
	log.Printf("Successfully uploaded file %s to OpenAI with ID: %s", fileID, openAIResp.ID)

	return openAIResp.ID, nil
}

// getFileInfo retrieves file information including filename
func getFileInfo(ocClient *client.Client, fileID string) (*struct {
	FileID       string                 `json:"file_id"`
	FileName     string                 `json:"file_name"`
	Size         int64                  `json:"size"`
	MimeType     string                 `json:"mime_type"`
	UploadedAt   string                 `json:"uploaded_at"`
	OpenAIFileID string                 `json:"openai_file_id,omitempty"`
	MetaData     map[string]interface{} `json:"meta_data,omitempty"`
}, error) {
	// Create request to get file info
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/info", ocClient.GetHost(), fileID), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", ocClient.GetSessionId()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file info request failed with status: %d", resp.StatusCode)
	}

	// Parse the response to get file metadata
	var fileInfo struct {
		FileID       string                 `json:"file_id"`
		FileName     string                 `json:"file_name"`
		Size         int64                  `json:"size"`
		MimeType     string                 `json:"mime_type"`
		UploadedAt   string                 `json:"uploaded_at"`
		OpenAIFileID string                 `json:"openai_file_id,omitempty"`
		MetaData     map[string]interface{} `json:"meta_data,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return nil, fmt.Errorf("error decoding file info: %w", err)
	}

	return &fileInfo, nil
}
