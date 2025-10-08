package integrations

import (
	"backend/api"
	"backend/api/msgmate"
	"backend/client"
	"backend/database"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SignalBotService handles Signal bot operations and AI processing
type SignalBotService struct {
	DB        *gorm.DB
	serverURL string
}

// NewSignalBotService creates a new Signal bot service
func NewSignalBotService(DB *gorm.DB, serverURL string) *SignalBotService {
	return &SignalBotService{
		DB:        DB,
		serverURL: serverURL,
	}
}

// SignalBotConfig holds configuration for Signal bot operations
type SignalBotConfig struct {
	// Flags to control behavior
	LogSignalEvents            bool
	DontRequireAICommandPrefix bool
}

// DefaultSignalBotConfig returns the default configuration
func DefaultSignalBotConfig() *SignalBotConfig {
	return &SignalBotConfig{
		LogSignalEvents:            false,
		DontRequireAICommandPrefix: false,
	}
}

// ProcessSignalMessages processes incoming Signal messages and handles AI interactions
func (sbs *SignalBotService) ProcessSignalMessages(integration database.Integration, config *SignalBotConfig) error {
	// Parse the integration config
	var integrationConfig map[string]interface{}
	if err := json.Unmarshal(integration.Config, &integrationConfig); err != nil {
		log.Printf("[SignalBot] Error parsing integration config: %v", err)
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	alias, ok := integrationConfig["alias"].(string)
	if !ok {
		log.Printf("[SignalBot] Invalid alias in integration config")
		return fmt.Errorf("invalid alias in integration config")
	}

	port, ok := integrationConfig["port"].(float64)
	if !ok {
		log.Printf("[SignalBot] Invalid port in integration config")
		return fmt.Errorf("invalid port in integration config")
	}

	phoneNumber, ok := integrationConfig["phone_number"].(string)
	if !ok {
		log.Printf("[SignalBot] Invalid phone_number in integration config")
		return fmt.Errorf("invalid phone_number in integration config")
	}

	log.Printf("[SignalBot:%s] Processing Signal messages - Phone: %s, Port: %d", alias, phoneNumber, int(port))

	// Construct the URL for the Signal REST API
	url := fmt.Sprintf("http://localhost:%d/v1/receive/%s", int(port), phoneNumber)
	log.Printf("[SignalBot:%s] Requesting messages from: %s", alias, url)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Make the request with retry logic
	var resp *http.Response
	var err error
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("[SignalBot:%s] Attempt %d of %d to connect to Signal REST API", alias, attempt, maxRetries)

		resp, err = client.Get(url)
		if err == nil {
			break // Success, exit retry loop
		}

		if attempt < maxRetries {
			retryDelay := time.Duration(attempt) * 2 * time.Second
			log.Printf("[SignalBot:%s] Connection failed, retrying in %v: %v", alias, retryDelay, err)
			time.Sleep(retryDelay)
		}
	}

	if err != nil {
		log.Printf("[SignalBot:%s] All connection attempts failed: %v", alias, err)
		return fmt.Errorf("failed to connect to Signal REST API after %d attempts: %w", maxRetries, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[SignalBot:%s] Signal REST API returned non-OK status: %d", alias, resp.StatusCode)
		// Read and log response body for more details
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("[SignalBot:%s] Response body: %s", alias, string(bodyBytes))
		return fmt.Errorf("Signal REST API returned non-OK status: %d", resp.StatusCode)
	}

	log.Printf("[SignalBot:%s] Received response with status: %d", alias, resp.StatusCode)

	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[SignalBot:%s] Failed to read response body: %v", alias, err)
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Log the raw JSON response
	log.Printf("[SignalBot:%s] Raw JSON response: %s", alias, string(bodyBytes))

	// Parse the messages
	var messages []map[string]interface{}
	if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&messages); err != nil {
		log.Printf("[SignalBot:%s] Failed to parse Signal messages: %v", alias, err)
		return fmt.Errorf("failed to parse Signal messages: %w", err)
	}

	log.Printf("[SignalBot:%s] Parsed %d messages", alias, len(messages))

	// Process each message
	for _, message := range messages {
		if err := sbs.processSignalMessage(message, integration, config); err != nil {
			log.Printf("[SignalBot:%s] Error processing message: %v", alias, err)
			continue
		}
	}

	// Update the last_used timestamp for the integration
	now := time.Now()
	integration.LastUsed = &now
	if err := sbs.DB.Save(&integration).Error; err != nil {
		log.Printf("[SignalBot:%s] Failed to update integration last_used: %v", alias, err)
	} else {
		log.Printf("[SignalBot:%s] Updated last_used timestamp", alias)
	}

	return nil
}

// processSignalMessage processes a single Signal message
func (sbs *SignalBotService) processSignalMessage(message map[string]interface{}, integration database.Integration, config *SignalBotConfig) error {
	// Parse integration config
	var integrationConfig map[string]interface{}
	if err := json.Unmarshal(integration.Config, &integrationConfig); err != nil {
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	alias, _ := integrationConfig["alias"].(string)
	phoneNumber, _ := integrationConfig["phone_number"].(string)

	// First, check if this is a group message and skip it early
	if sbs.isGroupMessage(message) {
		jsonBytes, _ := json.Marshal(message)
		log.Printf("[SignalBot:%s] Skipping group message: %s", alias, string(jsonBytes))
		return nil
	}

	var sourceNumber, destinationNumber, messageText string
	var timestamp int64
	var isTypingMessage, isReadReceipt, isBlockedMessage, isSyncMessage bool

	// Extract message details from envelope
	if envelope, ok := message["envelope"].(map[string]interface{}); ok {
		// Extract source number
		if source, ok := envelope["source"].(string); ok {
			sourceNumber = source
		} else if sourceNumber, ok := envelope["sourceNumber"].(string); ok {
			sourceNumber = sourceNumber
		}

		// Check for various message types
		// Check for syncMessage (when user sends a message)
		if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			isSyncMessage = true

			// Check for blocked contacts
			if blockedNumbers, ok := syncMessage["blockedNumbers"]; ok {
				isBlockedMessage = true
				if config.LogSignalEvents {
					// Create a descriptive message for blocked numbers event
					messageText = fmt.Sprintf("[signal-event] Blocked numbers updated: %v", blockedNumbers)
				}
			}

			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if text, ok := sentMessage["message"].(string); ok {
					messageText = text
				} else if config.LogSignalEvents {
					messageText = "[signal-event] Empty message sent"
				}

				if ts, ok := sentMessage["timestamp"].(float64); ok {
					timestamp = int64(ts)
				} else if ts, ok := envelope["timestamp"].(float64); ok {
					timestamp = int64(ts)
				}

				// Extract destination number for sync messages
				if destination, ok := sentMessage["destination"].(string); ok {
					destinationNumber = destination
				} else if destNumber, ok := sentMessage["destinationNumber"].(string); ok {
					destinationNumber = destNumber
				}
			} else if config.LogSignalEvents && messageText == "" {
				// Handle other sync message types
				syncMessageBytes, _ := json.Marshal(syncMessage)
				messageText = fmt.Sprintf("[signal-event] Sync event: %s", string(syncMessageBytes))
			}
		}

		// Check for dataMessage (when receiving a message from someone else)
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if text, ok := dataMessage["message"].(string); ok {
				messageText = text
			} else if config.LogSignalEvents && messageText == "" {
				dataMessageBytes, _ := json.Marshal(dataMessage)
				messageText = fmt.Sprintf("[signal-event] Data message event: %s", string(dataMessageBytes))
			}

			if ts, ok := dataMessage["timestamp"].(float64); ok {
				timestamp = int64(ts)
			}
		}

		// Also check for dataMessage at the content level for some message formats
		if content, ok := envelope["content"].(map[string]interface{}); ok {
			if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
				if text, ok := dataMessage["message"].(string); ok {
					messageText = text
				} else if config.LogSignalEvents && messageText == "" {
					dataMessageBytes, _ := json.Marshal(dataMessage)
					messageText = fmt.Sprintf("[signal-event] Content data message event: %s", string(dataMessageBytes))
				}

				if ts, ok := dataMessage["timestamp"].(float64); ok {
					timestamp = int64(ts)
				}
			}

			// Check for typing indicator
			if typingMessage, ok := content["typingMessage"].(map[string]interface{}); ok {
				isTypingMessage = true
				if config.LogSignalEvents {
					messageText = "[signal-event] Typing indicator"
				}

				if ts, ok := typingMessage["timestamp"].(float64); ok {
					timestamp = int64(ts)
				}
			}

			// Check for read receipts
			if receiptMessage, ok := content["receiptMessage"].(map[string]interface{}); ok {
				isReadReceipt = true
				if config.LogSignalEvents {
					messageText = "[signal-event] Read receipt"
				}

				if ts, ok := receiptMessage["timestamp"].(float64); ok {
					timestamp = int64(ts)
				}
			}
		}

		// If we still don't have a timestamp, try to get it from the envelope
		if timestamp == 0 {
			if ts, ok := envelope["timestamp"].(float64); ok {
				timestamp = int64(ts)
			}
		}
	}

	// Skip processing for typing indicators, read receipts, and other non-message events
	if (isTypingMessage || isReadReceipt || isBlockedMessage) && !config.LogSignalEvents {
		jsonBytes, _ := json.Marshal(message)
		log.Printf("[SignalBot:%s] Skipping non-message event: %s", alias, string(jsonBytes))
		return nil
	}

	// Skip if we don't have actual message text (unless it's a logged event)
	if messageText == "" && !(config.LogSignalEvents && (isTypingMessage || isReadReceipt || isBlockedMessage)) {
		log.Printf("[SignalBot:%s] Skipping message with empty text", alias)
		return nil
	}

	// Determine the conversation participants
	var conversationPartner string

	// If this is a sync message (sent by owner), use the destination as the conversation partner
	if isSyncMessage && destinationNumber != "" {
		conversationPartner = destinationNumber
		log.Printf("[SignalBot:%s] Processing sync message - From: %s, To: %s, Message: %s, Time: %s",
			alias,
			sourceNumber,
			destinationNumber,
			messageText,
			time.Unix(timestamp/1000, 0).Format(time.RFC3339))
	} else {
		// For received messages, use the source as the conversation partner
		conversationPartner = sourceNumber
		log.Printf("[SignalBot:%s] Processing received message - From: %s, Message: %s, Time: %s",
			alias,
			sourceNumber,
			messageText,
			time.Unix(timestamp/1000, 0).Format(time.RFC3339))
	}

	// Skip if conversation partner is empty
	if conversationPartner == "" {
		log.Printf("[SignalBot:%s] Skipping message with no clear conversation partner", alias)
		return nil
	}

	// Determine if the message is from the integration owner
	isFromOwner := isSyncMessage || sourceNumber == phoneNumber

	// Find the Signal user
	var signalUser database.User
	if err := sbs.DB.Where("name = ?", "signal").First(&signalUser).Error; err != nil {
		log.Printf("[SignalBot:%s] Error finding Signal user: %v", alias, err)
		return fmt.Errorf("error finding Signal user: %w", err)
	}

	// Find the integration owner
	var integrationOwner database.User
	if err := sbs.DB.First(&integrationOwner, integration.UserID).Error; err != nil {
		log.Printf("[SignalBot:%s] Error finding integration owner: %v", alias, err)
		return fmt.Errorf("error finding integration owner: %w", err)
	}

	// Process the message in a transaction
	err := sbs.DB.Transaction(func(tx *gorm.DB) error {
		// First, try to find an existing chat for this conversation partner and integration
		var chat database.Chat
		var chatFound bool

		// Look for a chat with shared config containing this conversation partner and alias
		var chats []database.Chat
		if err := tx.Preload("User1").Preload("User2").Preload("SharedConfig").
			Where("(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
				signalUser.ID, integrationOwner.ID, integrationOwner.ID, signalUser.ID).
			Find(&chats).Error; err != nil {
			return err
		}

		// Check each chat's shared config to find one for this conversation partner and alias
		for _, c := range chats {
			if c.SharedConfig != nil {
				var configData map[string]interface{}
				if err := json.Unmarshal(c.SharedConfig.ConfigData, &configData); err == nil {
					configAlias, aliasOk := configData["signal_alias"].(string)
					configPhone, phoneOk := configData["signal_phone"].(string)

					if aliasOk && phoneOk && configAlias == alias && configPhone == conversationPartner {
						chat = c
						chatFound = true
						break
					}
				}
			}
		}

		// If no chat found, create a new one
		if !chatFound {
			log.Printf("[SignalBot:%s] Creating new chat for conversation partner %s", alias, conversationPartner)

			// Create a new chat between the Signal user and the integration owner
			var newChat database.Chat
			if signalUser.ID < integrationOwner.ID {
				newChat = database.Chat{
					User1Id:  signalUser.ID,
					User2Id:  integrationOwner.ID,
					ChatType: "integration:signal",
				}
			} else {
				newChat = database.Chat{
					User1Id:  integrationOwner.ID,
					User2Id:  signalUser.ID,
					ChatType: "integration:signal",
				}
			}

			if err := tx.Create(&newChat).Error; err != nil {
				return err
			}

			// Create shared config for the chat with conversation partner and alias
			configData := map[string]interface{}{
				"signal_phone":   conversationPartner,
				"signal_alias":   alias,
				"integration_id": integration.ID,
			}

			configBytes, err := json.Marshal(configData)
			if err != nil {
				return err
			}

			sharedConfig := database.SharedChatConfig{
				ChatId:     newChat.ID,
				ConfigData: configBytes,
			}

			if err := tx.Create(&sharedConfig).Error; err != nil {
				return err
			}

			// Update chat with shared config
			newChat.SharedConfigId = &sharedConfig.ID
			if err := tx.Save(&newChat).Error; err != nil {
				return err
			}

			// Load the chat with all relationships
			if err := tx.Preload("User1").Preload("User2").Preload("SharedConfig").
				First(&chat, newChat.ID).Error; err != nil {
				return err
			}
		} else {
			log.Printf("[SignalBot:%s] Found existing chat for conversation partner %s", alias, conversationPartner)
			log.Printf("[SignalBot:%s] Chat type: %s", alias, chat.ChatType)
			log.Printf("[SignalBot:%s] Chat UUID: %s", alias, chat.UUID)
			log.Printf("[SignalBot:%s] Chat ID: %d", alias, chat.ID)
		}

		// Determine sender and receiver based on message source
		var senderId, receiverId uint
		if isFromOwner {
			senderId = integrationOwner.ID
			receiverId = signalUser.ID
		} else {
			senderId = signalUser.ID
			receiverId = integrationOwner.ID
		}

		// Create the message
		dbMessage := database.Message{
			ChatId:     chat.ID,
			SenderId:   senderId,
			ReceiverId: receiverId,
			Text:       &messageText,
			DataType:   "text",
		}

		// Add metadata about the signal message
		metaData := map[string]interface{}{
			"signal_source":      sourceNumber,
			"signal_destination": destinationNumber,
			"signal_timestamp":   timestamp,
			"signal_alias":       alias,
			"is_sync_message":    isSyncMessage,
		}

		metaDataBytes, err := json.Marshal(metaData)
		if err != nil {
			return err
		}

		dbMessage.MetaData = metaDataBytes

		// Save the message
		if err := tx.Create(&dbMessage).Error; err != nil {
			return err
		}

		// Update the chat's latest message
		chat.LatestMessageId = &dbMessage.ID
		if err := tx.Save(&chat).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		log.Printf("[SignalBot:%s] Error processing message: %v", alias, err)
		return err
	}

	// Determine if this message should trigger AI processing
	isToOwner := false
	if isSyncMessage {
		// For sync messages, check if destination matches owner's number
		isToOwner = destinationNumber == phoneNumber
	} else {
		// For received messages, the destination is implicitly the owner's number (integration number)
		isToOwner = true
	}

	// Determine if this is a command message (starts with '/')
	isCommand := len(messageText) > 0 && messageText[0] == '/'

	// Process command if this is a command message
	if isCommand {
		if isToOwner {
			parts := strings.SplitN(messageText[1:], " ", 2)
			command := parts[0]
			var commandText string
			if len(parts) > 1 {
				commandText = parts[1]
			} else {
				commandText = ""
			}

			log.Printf("[SignalBot:%s] Processing command message: /%s %s", alias, command, commandText)

			// Process the command
			if err := sbs.processSignalCommand(command, commandText, sourceNumber, alias, integration, config); err != nil {
				log.Printf("[SignalBot:%s] Error processing command: %v", alias, err)
			} else {
				log.Printf("[SignalBot:%s] Successfully processed command: %s", alias, command)
			}
		} else {
			log.Printf("[SignalBot:%s] Command message not directed to owner, skipping command processing", alias)
		}
	} else if config.DontRequireAICommandPrefix && isToOwner {
		// Check whitelist status with detailed logging
		isWhitelisted := sbs.isNumberInWhitelist(sourceNumber, integration)
		log.Printf("[SignalBot:%s] Message from %s to owner, whitelist check result: %v", alias, sourceNumber, isWhitelisted)

		if isWhitelisted {
			// If the flag is enabled and number is whitelisted, process non-command messages as AI commands
			log.Printf("[SignalBot:%s] Processing non-command message as AI command from whitelisted number %s: %s", alias, sourceNumber, messageText)
			if err := sbs.processAICommand(messageText, sourceNumber, alias, integration, nil, config); err != nil {
				log.Printf("[SignalBot:%s] Error processing message as AI command: %v", alias, err)
			} else {
				log.Printf("[SignalBot:%s] Successfully processed message as AI command", alias)
			}
		} else {
			log.Printf("[SignalBot:%s] Not processing message as AI command because number %s is not whitelisted", alias, sourceNumber)
		}
	} else {
		if config.DontRequireAICommandPrefix {
			if !isToOwner {
				log.Printf("[SignalBot:%s] Message not directed to owner, skipping AI processing", alias)
			} else {
				log.Printf("[SignalBot:%s] Message directed to owner but number %s not whitelisted, skipping AI processing", alias, sourceNumber)
			}
		} else {
			log.Printf("[SignalBot:%s] AI command prefix required but not present, skipping AI processing", alias)
		}
	}

	// Send websocket notification if available
	// This would require access to the websocket handler
	// For now, we'll just log that we would send a notification
	log.Printf("[SignalBot:%s] Would send websocket notification for new message", alias)

	return nil
}

// isGroupMessage checks if a message is a group message
func (sbs *SignalBotService) isGroupMessage(message map[string]interface{}) bool {
	// Check if this is a group message by looking for groupInfo or groupId
	if envelope, ok := message["envelope"].(map[string]interface{}); ok {
		// Check for groupInfo in dataMessage
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if groupInfo, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
				return groupInfo != nil
			}
		}

		// Check for groupId in content.dataMessage.groupInfo
		if content, ok := envelope["content"].(map[string]interface{}); ok {
			if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
				if groupInfo, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
					return groupInfo != nil
				}
			}

			// Check for groupId in typingMessage
			if typingMessage, ok := content["typingMessage"].(map[string]interface{}); ok {
				if _, ok := typingMessage["groupId"].(string); ok {
					return true
				}
			}
		}

		// Check for syncMessage with groupInfo
		if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if groupInfo, ok := sentMessage["groupInfo"].(map[string]interface{}); ok {
					return groupInfo != nil
				}
			}
		}
	}

	return false
}

// isNumberInWhitelist checks if a number is in the whitelist
func (sbs *SignalBotService) isNumberInWhitelist(number string, integration database.Integration) bool {
	// Parse the integration config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		log.Printf("Error parsing integration config: %v", err)
		return false
	}

	// Get the whitelist from the config
	if whitelistInterface, exists := config["whitelist"]; exists {
		if whitelistArray, ok := whitelistInterface.([]interface{}); ok {
			// If whitelist is empty, don't auto-process as AI
			if len(whitelistArray) == 0 {
				log.Printf("Whitelist exists but is empty, not auto-processing as AI")
				return false
			}

			// Check if the number is in the whitelist
			for _, item := range whitelistArray {
				if str, ok := item.(string); ok && str == number {
					log.Printf("Number %s found in whitelist", number)
					return true
				}
			}

			log.Printf("Number %s not found in whitelist of %d entries", number, len(whitelistArray))
		} else {
			log.Printf("Whitelist is not an array, cannot check if number is whitelisted")
		}
	} else {
		// No whitelist at all
		log.Printf("No whitelist found in integration config")
		return false
	}

	return false
}

// processSignalCommand processes a Signal command
func (sbs *SignalBotService) processSignalCommand(command string, message string, sourceNumber string, alias string, integration database.Integration, config *SignalBotConfig) error {
	log.Printf("[SignalBot:%s] Processing command: %s from number: %s", alias, command, sourceNumber)

	switch command {
	case "ai":
		log.Printf("[SignalBot:%s] Recognized AI command, processing with message: %s", alias, message)
		return sbs.processAICommand(message, sourceNumber, alias, integration, nil, config)
	default:
		if config.DontRequireAICommandPrefix {
			// If the flag is enabled, treat unknown commands as AI commands
			fullMessage := "/" + command
			if message != "" {
				fullMessage += " " + message
			}
			log.Printf("[SignalBot:%s] Processing unknown command as AI command: %s from number: %s", alias, fullMessage, sourceNumber)
			return sbs.processAICommand(fullMessage, sourceNumber, alias, integration, nil, config)
		}
		log.Printf("[SignalBot:%s] Unknown command: %s from number: %s, not processing as AI", alias, command, sourceNumber)
		return fmt.Errorf("unknown command: %s", command)
	}
}

func (sbs *SignalBotService) logFunctionInfo(alias string, initData map[string]interface{}, inputData map[string]interface{}) {
	log.Printf("[SignalBot:%s] ============================= TBS =============== TBS ================= Signal interaction start", alias)
	log.Printf("[SignalBot:%s] Init data: %+v", alias, initData)
	log.Printf("[SignalBot:%s] Input data: %+v", alias, inputData)
	log.Printf("[SignalBot:%s] ============================= TBS =============== TBS ================= Signal interaction start", alias)
}

// processAICommand processes an AI command with the Signal integration
func (sbs *SignalBotService) processAICommand(message string, sourceNumber string, alias string, integration database.Integration, attachments []map[string]interface{}, config *SignalBotConfig) error {
	log.Printf("[SignalBot:%s] Processing AI command with message: %s from number: %s", alias, message, sourceNumber)
	log.Printf("[SignalBot:%s] Number of attachments received: %d", alias, len(attachments))
	if len(attachments) > 0 {
		log.Printf("[SignalBot:%s] Attachment details: %+v", alias, attachments)
	}

	// Parse the integration config to get the port and phone number
	var integrationConfig map[string]interface{}
	if err := json.Unmarshal(integration.Config, &integrationConfig); err != nil {
		log.Printf("[SignalBot:%s] Failed to parse integration config: %v", alias, err)
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	port, ok := integrationConfig["port"].(float64)
	if !ok {
		log.Printf("[SignalBot:%s] Invalid port in integration config", alias)
		return fmt.Errorf("invalid port in integration config")
	}

	phoneNumber, ok := integrationConfig["phone_number"].(string)
	if !ok {
		log.Printf("[SignalBot:%s] Invalid phone_number in integration config", alias)
		return fmt.Errorf("invalid phone_number in integration config")
	}

	log.Printf("[SignalBot:%s] Using port: %v and phone number: %s", alias, port, phoneNumber)

	// Find the integration owner
	var integrationOwner database.User
	if err := sbs.DB.First(&integrationOwner, integration.UserID).Error; err != nil {
		return fmt.Errorf("failed to find integration owner: %w", err)
	}

	// Find the Signal user
	var signalUser database.User
	if err := sbs.DB.Where("name = ?", "signal").First(&signalUser).Error; err != nil {
		return fmt.Errorf("failed to find Signal user: %w", err)
	}

	// Find the chat for this Signal conversation
	var chatUUID string
	var chats []database.Chat
	if err := sbs.DB.Preload("SharedConfig").
		Where("(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
			signalUser.ID, integrationOwner.ID, integrationOwner.ID, signalUser.ID).
		Find(&chats).Error; err != nil {
		return fmt.Errorf("failed to find chats: %w", err)
	}

	// Find the specific chat for this phone number and alias
	for _, chat := range chats {
		if chat.SharedConfig != nil {
			var configData map[string]interface{}
			if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
				configAlias, aliasOk := configData["signal_alias"].(string)
				configPhone, phoneOk := configData["signal_phone"].(string)

				if aliasOk && phoneOk && configAlias == alias && configPhone == sourceNumber {
					chatUUID = chat.UUID
					break
				}
			}
		}
	}

	if chatUUID == "" {
		return fmt.Errorf("could not find chat UUID for this Signal conversation")
	}

	// Generate a session token for the integration owner
	token := api.GenerateToken(integrationOwner.Email)
	expiry := time.Now().Add(24 * time.Hour)

	// Create a session in the database
	session := database.Session{
		Token:  token,
		Data:   []byte{},
		Expiry: expiry,
		UserId: integrationOwner.ID,
	}

	if err := sbs.DB.Create(&session).Error; err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Generate a session token for the Signal user
	signalUserToken := api.GenerateToken(signalUser.Email)
	signalUserExpiry := time.Now().Add(24 * time.Hour)

	// Create a session in the database for the Signal user
	signalUserSession := database.Session{
		Token:  signalUserToken,
		Data:   []byte{},
		Expiry: signalUserExpiry,
		UserId: signalUser.ID,
	}

	if err := sbs.DB.Create(&signalUserSession).Error; err != nil {
		return fmt.Errorf("failed to create session for Signal user: %w", err)
	}

	// Create a LocalInteractionClient with the server URL and session token
	interactionClient, err := msgmate.NewLocalInteractionClient(sbs.serverURL, token)
	if err != nil {
		log.Printf("[SignalBot:%s] Failed to create interaction client: %v", alias, err)
		return fmt.Errorf("failed to create interaction client: %w", err)
	}

	// Define the tools to use based on whether this is admin (source == integration phone)
	toolsList := []string{
		//"signal_send_message",
		"signal_read_past_messages",
		//"signal_show_typing_indicator",
		"get_current_time",
		"interaction_start:run_callback_function",
		"interaction_complete:run_callback_function",
	}

	// Check if the source number is the same as the integration's phone number
	// If so, add the whitelist management tools
	isAdmin := sourceNumber == phoneNumber
	if isAdmin {
		log.Printf("[SignalBot:%s] Source number matches integration number, enabling admin tools", alias)
		toolsList = append(toolsList,
			"signal_get_whitelist",
			"signal_add_to_whitelist",
			"signal_remove_from_whitelist")
	}

	useN8n := os.Getenv("N8N_ENABLE_BOT_ENDPOINT") == "true"
	if useN8n {
		// Validate required N8N environment variables
		if os.Getenv("N8N_DEFAULT_BOT_ENDPOINT") == "" {
			log.Printf("[SignalBot:%s] Warning: N8N_DEFAULT_BOT_ENDPOINT not set, disabling N8N integration", alias)
			useN8n = false
		} else if os.Getenv("N8N_DEFAULT_BOT_ENDPOINT_USER") == "" {
			log.Printf("[SignalBot:%s] Warning: N8N_DEFAULT_BOT_ENDPOINT_USER not set, disabling N8N integration", alias)
			useN8n = false
		} else if os.Getenv("N8N_DEFAULT_BOT_ENDPOINT_PASSWORD") == "" {
			log.Printf("[SignalBot:%s] Warning: N8N_DEFAULT_BOT_ENDPOINT_PASSWORD not set, disabling N8N integration", alias)
			useN8n = false
		} else {
			toolsList = append(toolsList, "n8n_trigger_workflow_webhook")
		}
	}

	// Configure the bot
	botConfig := map[string]interface{}{
		"temperature": 0.7,
		"max_tokens":  4096,
		"model":       "gpt-4o",
		"endpoint":    "https://api.openai.com/v1/",
		"backend":     "openai",
		"context":     10,
		"system_prompt": "You are a helpful assistant in a chat conversation.\n\n" +
			"GUIDELINES:\n" +
			"- Always respond in the same language the user wrote their message in.\n" +
			"- Be concise and direct in your communication.\n" +
			"- Provide helpful and clear information.\n" +
			"- Use appropriate formatting with paragraphs, bullet points, or lists when needed.\n" +
			"- Focus on providing valuable responses to user queries.\n\n" +
			"- If you are missing some context use the signal_read_past_messages tool to get the latest messages in the chat.\n\n" +
			"Note: The typing indicator and message sending are handled automatically by the system.",
		"tools": toolsList,
	}
	if useN8n {
		botConfig["tags"] = []string{"skip-core"}
	}
	interactionClient.SetBotConfig(botConfig)

	// Create a common configuration for base tools
	commonToolConfig := map[string]interface{}{
		"recipient_phone":        sourceNumber, // Send response back to the source number
		"sender_phone":           phoneNumber,  // Send from the integration's phone number
		"api_host":               fmt.Sprintf("http://localhost:%d", int(port)),
		"chat_uuid":              chatUUID,
		"backend_host":           sbs.serverURL,
		"signal_user_session_id": signalUserToken,
		"signal_user_uuid":       signalUser.UUID,
	}

	n8nToolConfig := map[string]interface{}{
		"api_endpoint": os.Getenv("N8N_DEFAULT_BOT_ENDPOINT"),
		"api_user":     os.Getenv("N8N_DEFAULT_BOT_ENDPOINT_USER"),
		"api_password": os.Getenv("N8N_DEFAULT_BOT_ENDPOINT_PASSWORD"),
	}

	var startFuncId string
	var stopFuncId string

	if useN8n {
		startFuncId, err = msgmate.GetGlobalMsgmateHandler().QuickRegisterFunction(
			func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error) {
				sbs.logFunctionInfo(alias, initData, inputData)
				showTypingIndicatorTool := msgmate.GetNewToolInstanceByName("signal_show_typing_indicator", commonToolConfig)
				sendSignalMessageTool := msgmate.GetNewToolInstanceByName("signal_send_message", commonToolConfig)
				n8nTriggerWorkflowWebhookTool := msgmate.GetNewToolInstanceByName("n8n_trigger_workflow_webhook", n8nToolConfig)
				n8nTriggerWorkflowWebhookTool.RunTool(msgmate.N8NTriggerWorkflowWebhookToolInput{
					InputParameters: map[string]interface{}{
						"available_tools": toolsList,
					},
				})
				sendSignalMessageTool.RunTool(msgmate.SignalSendMessageToolInput{
					Message: "Starting n8n bot workflow...",
				})
				showTypingIndicatorTool.RunTool(msgmate.SignalShowTypingIndicatorToolInput{})
				return nil, nil
			})
		stopFuncId, err = msgmate.GetGlobalMsgmateHandler().QuickRegisterFunction(
			func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error) {
				sbs.logFunctionInfo(alias, initData, inputData)
				sendSignalMessageTool := msgmate.GetNewToolInstanceByName("signal_send_message", commonToolConfig)
				sendSignalMessageTool.RunTool(msgmate.SignalSendMessageToolInput{
					Message: "Thank you for your message. I'm here to help!",
				})
				return nil, nil
			})
	} else {
		startFuncId, err = msgmate.GetGlobalMsgmateHandler().QuickRegisterFunction(
			func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error) {
				sbs.logFunctionInfo(alias, initData, inputData)
				showTypingIndicatorTool := msgmate.GetNewToolInstanceByName("signal_show_typing_indicator", commonToolConfig)
				showTypingIndicatorTool.RunTool(msgmate.SignalShowTypingIndicatorToolInput{})
				return nil, nil
			})

		stopFuncId, err = msgmate.GetGlobalMsgmateHandler().QuickRegisterFunction(
			func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error) {
				sbs.logFunctionInfo(alias, initData, inputData)
				sendSignalMessageTool := msgmate.GetNewToolInstanceByName("signal_send_message", commonToolConfig)
				sendSignalMessageTool.RunTool(msgmate.SignalSendMessageToolInput{
					Message: inputData["last_ai_message"].(string),
				})
				return nil, nil
			})
	}

	if err != nil {
		log.Printf("[SignalBot:%s] Failed to register signal interaction start function: %v", alias, err)
		return fmt.Errorf("failed to register signal interaction start function: %w", err)
	}

	// Initialize tools
	toolInit := map[string]interface{}{
		"signal_send_message":                        commonToolConfig,
		"signal_read_past_messages":                  commonToolConfig,
		"signal_show_typing_indicator":               commonToolConfig,
		"interaction_start:run_callback_function":    map[string]interface{}{"callback_function_id": startFuncId},
		"interaction_complete:run_callback_function": map[string]interface{}{"callback_function_id": stopFuncId},
	}

	// If the source is the admin, initialize the whitelist management tools
	if isAdmin {
		// Create the whitelist tool config with additional admin parameters
		whitelistToolConfig := map[string]interface{}{}

		// Copy all fields from commonToolConfig
		for k, v := range commonToolConfig {
			whitelistToolConfig[k] = v
		}

		// Add the additional parameters required for whitelist management
		whitelistToolConfig["admin_user_session_id"] = token // Use the integration owner's token
		whitelistToolConfig["integration_alias"] = alias     // Set the integration alias

		// Initialize the whitelist tools
		toolInit["signal_get_whitelist"] = whitelistToolConfig
		toolInit["signal_add_to_whitelist"] = whitelistToolConfig
		toolInit["signal_remove_from_whitelist"] = whitelistToolConfig
	}

	// Process attachments if any
	var processedMessage string
	if len(attachments) > 0 {
		log.Printf("[SignalBot:%s] Processing %d attachments for AI interaction", alias, len(attachments))

		// Create content array with text and file references (similar to StartBot)
		contentArray := []map[string]interface{}{}

		// Create file mapping for LocalInteractionClient
		fileMapping := make(map[string]interface{})

		// Add text content if it exists
		if message != "" {
			contentArray = append(contentArray, map[string]interface{}{
				"type": "text",
				"text": message,
			})
		}

		// Add each file attachment
		for i, attachment := range attachments {
			log.Printf("[SignalBot:%s] Processing attachment %d: %+v", alias, i, attachment)

			fileID, ok := attachment["file_id"].(string)
			if !ok {
				log.Printf("[SignalBot:%s] Skipping attachment without file_id", alias)
				continue
			}

			// Check if backend is OpenAI and if openai_file_id is present
			backend := "openai" // Default to OpenAI for Signal integration
			openaiFileID, hasOpenAIID := attachment["openai_file_id"].(string)

			if backend == "openai" && hasOpenAIID && openaiFileID != "" {
				// Use existing OpenAI file ID
				log.Printf("[SignalBot:%s] Using existing OpenAI file ID: %s", alias, openaiFileID)
				contentArray = append(contentArray, map[string]interface{}{
					"type":    "file",
					"file_id": openaiFileID,
				})
				// Store mapping: OpenAI file ID -> internal file ID
				fileMapping[openaiFileID] = fileID
				log.Printf("[SignalBot:%s] Stored file mapping: %s -> %s", alias, openaiFileID, fileID)
			} else if backend == "openai" {
				// Upload file to OpenAI if not already uploaded
				log.Printf("[SignalBot:%s] Uploading file %s to OpenAI", alias, fileID)

				// Create a client for file operations
				fileClient := client.NewClient(sbs.serverURL)
				fileClient.SetSessionId(token)

				// Get file info
				_, mimeType, err := sbs.retrieveFileData(fileClient, fileID)
				if err != nil {
					log.Printf("[SignalBot:%s] Failed to retrieve file data for %s: %v", alias, fileID, err)
					continue
				}

				// Upload to OpenAI
				openaiFileID, err = sbs.uploadFileToOpenAI(fileClient, fileID, mimeType)
				if err != nil {
					log.Printf("[SignalBot:%s] Failed to upload file %s to OpenAI: %v", alias, fileID, err)
					continue
				}

				log.Printf("[SignalBot:%s] Successfully uploaded file %s to OpenAI with ID: %s", alias, fileID, openaiFileID)

				contentArray = append(contentArray, map[string]interface{}{
					"type":    "file",
					"file_id": openaiFileID,
				})
				// Store mapping: OpenAI file ID -> internal file ID
				fileMapping[openaiFileID] = fileID
				log.Printf("[SignalBot:%s] Stored file mapping: %s -> %s", alias, openaiFileID, fileID)
			} else {
				// For non-OpenAI backends, use file_id directly
				contentArray = append(contentArray, map[string]interface{}{
					"type":    "file",
					"file_id": fileID,
				})
				// Store mapping: file_id -> file_id (same for non-OpenAI backends)
				fileMapping[fileID] = fileID
				log.Printf("[SignalBot:%s] Stored file mapping: %s -> %s", alias, fileID, fileID)
			}
		}

		log.Printf("[SignalBot:%s] Final content array: %+v", alias, contentArray)
		log.Printf("[SignalBot:%s] File mapping: %+v", alias, fileMapping)

		// Store file mapping in toolInit for LocalInteractionClient
		toolInit["file_mapping"] = fileMapping

		// Convert content array to JSON string for the message
		contentJSON, err := json.Marshal(contentArray)
		if err != nil {
			log.Printf("[SignalBot:%s] Failed to marshal content array: %v", alias, err)
			processedMessage = message // Fallback to original message
		} else {
			processedMessage = string(contentJSON)
			log.Printf("[SignalBot:%s] Final processed message: %s", alias, processedMessage)
		}
	} else {
		processedMessage = message
	}

	// Create the interaction with the AI
	interaction, err := interactionClient.CreateInteraction(toolInit, processedMessage)
	if err != nil {
		log.Printf("[SignalBot:%s] Failed to create AI interaction: %v", alias, err)
		return fmt.Errorf("failed to create AI interaction: %w", err)
	}

	log.Printf("[SignalBot:%s] Created AI interaction with UUID: %s", alias, interaction["uuid"])

	// Add more logging before returning
	log.Printf("[SignalBot:%s] AI interaction setup complete for message from %s", alias, sourceNumber)
	return nil
}

// retrieveFileData retrieves file data from the server
func (sbs *SignalBotService) retrieveFileData(ocClient *client.Client, fileID string) ([]byte, string, error) {
	// Get file data from the server using the new data endpoint
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/data", ocClient.GetHost(), fileID), nil)
	if err != nil {
		return nil, "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ocClient.GetHost())
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", ocClient.GetSessionId()))

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("error response: %v", resp.Status)
	}

	var response struct {
		Data        string `json:"data"`
		ContentType string `json:"content_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, "", fmt.Errorf("error decoding response: %w", err)
	}

	// Decode base64 data back to bytes
	fileBytes, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, "", fmt.Errorf("error decoding base64 data: %w", err)
	}

	return fileBytes, response.ContentType, nil
}

// uploadFileToOpenAI uploads a file to OpenAI's files API
func (sbs *SignalBotService) uploadFileToOpenAI(ocClient *client.Client, fileID string, mimeType string) (string, error) {
	// Get OpenAI API key
	openAIKey := ocClient.GetApiKey("openai")
	if openAIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	// First, get the file data from the server
	fileBytes, _, err := sbs.retrieveFileData(ocClient, fileID)
	if err != nil {
		return "", fmt.Errorf("error retrieving file data: %w", err)
	}

	// Get file info to get the filename
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/info", ocClient.GetHost(), fileID), nil)
	if err != nil {
		return "", fmt.Errorf("error creating file info request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ocClient.GetHost())
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", ocClient.GetSessionId()))

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending file info request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error response from file info: %v", resp.Status)
	}

	var fileInfo struct {
		FileName string `json:"file_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return "", fmt.Errorf("error decoding file info response: %w", err)
	}

	// Create multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file
	part, err := writer.CreateFormFile("file", fileInfo.FileName)
	if err != nil {
		return "", fmt.Errorf("error creating form file: %w", err)
	}
	part.Write(fileBytes)

	// Add the purpose
	writer.WriteField("purpose", "assistants")

	writer.Close()

	// Create the request to OpenAI
	req, err = http.NewRequest("POST", "https://api.openai.com/v1/files", body)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+openAIKey)

	// Send the request
	resp, err = httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("error response from OpenAI: %v - %s", resp.Status, string(bodyBytes))
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

	log.Printf("Successfully uploaded file %s to OpenAI with ID: %s", fileID, openAIResp.ID)

	return openAIResp.ID, nil
}
