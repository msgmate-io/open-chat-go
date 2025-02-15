package integrations

import (
	"backend/database"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// MatrixBotService handles Matrix bot operations and message processing
type MatrixBotService struct {
	DB        *gorm.DB
	serverURL string
}

// NewMatrixBotService creates a new Matrix bot service
func NewMatrixBotService(DB *gorm.DB, serverURL string) *MatrixBotService {
	return &MatrixBotService{
		DB:        DB,
		serverURL: serverURL,
	}
}

// MatrixBotConfig holds configuration for Matrix bot operations
type MatrixBotConfig struct {
	LogMatrixEvents            bool
	DontRequireAICommandPrefix bool
}

// DefaultMatrixBotConfig returns the default configuration
func DefaultMatrixBotConfig() *MatrixBotConfig {
	return &MatrixBotConfig{
		LogMatrixEvents:            false,
		DontRequireAICommandPrefix: false,
	}
}

// ProcessMessage processes a Matrix message event
func (mbs *MatrixBotService) ProcessMessage(conn *MatrixClientConnection, evt *event.Event, content *event.MessageEventContent) error {
	// Parse the integration config
	var integrationConfig map[string]interface{}
	if err := json.Unmarshal(conn.Integration.Config, &integrationConfig); err != nil {
		log.Printf("[MatrixBot:%d] Error parsing integration config: %v", conn.Integration.ID, err)
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	userID, _ := integrationConfig["user_id"].(string)
	log.Printf("[MatrixBot:%d] Processing message from %s in room %s: %s",
		conn.Integration.ID, evt.Sender, evt.RoomID, content.Body)

	// Find the Matrix user
	var matrixUser database.User
	if err := mbs.DB.Where("name = ?", MatrixUserName).First(&matrixUser).Error; err != nil {
		log.Printf("[MatrixBot:%d] Error finding Matrix user: %v", conn.Integration.ID, err)
		return fmt.Errorf("error finding Matrix user: %w", err)
	}

	// Find the integration owner
	var integrationOwner database.User
	if err := mbs.DB.First(&integrationOwner, conn.Integration.UserID).Error; err != nil {
		log.Printf("[MatrixBot:%d] Error finding integration owner: %v", conn.Integration.ID, err)
		return fmt.Errorf("error finding integration owner: %w", err)
	}

	// Find or create the Matrix room record
	var matrixRoom database.MatrixRoom
	roomResult := mbs.DB.Where("room_id = ? AND matrix_client_state_id = ?", string(evt.RoomID), conn.ClientState.ID).First(&matrixRoom)
	if roomResult.Error != nil {
		// Create new room record
		matrixRoom = database.MatrixRoom{
			MatrixClientStateID: conn.ClientState.ID,
			RoomID:              string(evt.RoomID),
			IsEncrypted:         conn.ClientState.DeviceVerified, // Assume encrypted if device is verified
		}
		if err := mbs.DB.Create(&matrixRoom).Error; err != nil {
			log.Printf("[MatrixBot:%d] Error creating room record: %v", conn.Integration.ID, err)
		}
	}

	// Determine if message is from owner
	isFromOwner := string(evt.Sender) == userID

	// Process the message in a transaction
	err := mbs.DB.Transaction(func(tx *gorm.DB) error {
		// Find or create chat for this room
		var chat database.Chat
		var chatFound bool

		// Look for existing chat with this room
		var chats []database.Chat
		if err := tx.Preload("SharedConfig").
			Where("(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
				matrixUser.ID, integrationOwner.ID, integrationOwner.ID, matrixUser.ID).
			Find(&chats).Error; err != nil {
			return err
		}

		// Find the specific chat for this room
		for _, c := range chats {
			if c.SharedConfig != nil {
				var configData map[string]interface{}
				if err := json.Unmarshal(c.SharedConfig.ConfigData, &configData); err == nil {
					configRoomID, roomOk := configData["matrix_room_id"].(string)
					configIntegrationID, intOk := configData["integration_id"].(float64)

					if roomOk && intOk && configRoomID == string(evt.RoomID) && uint(configIntegrationID) == conn.Integration.ID {
						chat = c
						chatFound = true
						break
					}
				}
			}
		}

		// If no chat found, create a new one
		if !chatFound {
			log.Printf("[MatrixBot:%d] Creating new chat for room %s", conn.Integration.ID, evt.RoomID)

			// Create the chat
			if matrixUser.ID < integrationOwner.ID {
				chat = database.Chat{
					User1Id:  matrixUser.ID,
					User2Id:  integrationOwner.ID,
					ChatType: "integration:matrix",
				}
			} else {
				chat = database.Chat{
					User1Id:  integrationOwner.ID,
					User2Id:  matrixUser.ID,
					ChatType: "integration:matrix",
				}
			}

			if err := tx.Create(&chat).Error; err != nil {
				return err
			}

			// Create shared config
			configData := map[string]interface{}{
				"matrix_room_id":   string(evt.RoomID),
				"integration_id":   conn.Integration.ID,
				"integration_uuid": conn.Integration.UUID,
				"sender_id":        string(evt.Sender),
			}

			configBytes, err := json.Marshal(configData)
			if err != nil {
				return err
			}

			sharedConfig := database.SharedChatConfig{
				ChatId:     chat.ID,
				ConfigData: configBytes,
			}

			if err := tx.Create(&sharedConfig).Error; err != nil {
				return err
			}

			chat.SharedConfigId = &sharedConfig.ID
			if err := tx.Save(&chat).Error; err != nil {
				return err
			}

			// Link chat to room
			matrixRoom.ChatID = &chat.ID
			tx.Save(&matrixRoom)
		}

		// Determine sender and receiver
		var senderId, receiverId uint
		if isFromOwner {
			senderId = integrationOwner.ID
			receiverId = matrixUser.ID
		} else {
			senderId = matrixUser.ID
			receiverId = integrationOwner.ID
		}

		// Create the message
		messageText := content.Body
		dbMessage := database.Message{
			ChatId:     chat.ID,
			SenderId:   senderId,
			ReceiverId: receiverId,
			Text:       &messageText,
			DataType:   "text",
		}

		// Add metadata
		metaData := map[string]interface{}{
			"matrix_sender":     string(evt.Sender),
			"matrix_room_id":    string(evt.RoomID),
			"matrix_event_id":   string(evt.ID),
			"matrix_timestamp":  evt.Timestamp,
			"is_from_owner":     isFromOwner,
			"integration_id":    conn.Integration.ID,
		}

		metaDataBytes, err := json.Marshal(metaData)
		if err != nil {
			return err
		}

		dbMessage.MetaData = metaDataBytes

		if err := tx.Create(&dbMessage).Error; err != nil {
			return err
		}

		// Update chat's latest message
		chat.LatestMessageId = &dbMessage.ID
		if err := tx.Save(&chat).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		log.Printf("[MatrixBot:%d] Error processing message: %v", conn.Integration.ID, err)
		return err
	}

	// Check if we should process this as an AI command
	shouldProcess := false
	messageText := content.Body

	// Check for command prefix
	hasAIPrefix := strings.HasPrefix(strings.ToLower(messageText), "/ai")

	// Check if sender is in whitelist
	isWhitelisted := mbs.isInWhitelist(string(evt.Sender), conn.Integration)

	// Get bot config
	config := DefaultMatrixBotConfig()
	if dontRequire, ok := integrationConfig["dont_require_ai_prefix"].(bool); ok {
		config.DontRequireAICommandPrefix = dontRequire
	}

	// Determine if we should process
	if !isFromOwner {
		if hasAIPrefix {
			shouldProcess = true
			messageText = strings.TrimSpace(strings.TrimPrefix(messageText, "/ai"))
		} else if config.DontRequireAICommandPrefix && isWhitelisted {
			shouldProcess = true
		}
	}

	if shouldProcess {
		log.Printf("[MatrixBot:%d] Processing message as AI command from %s", conn.Integration.ID, evt.Sender)
		// AI processing would go here - similar to Signal bot service
		// For now, we just log that it would be processed
	}

	// Update room's last activity
	now := time.Now()
	matrixRoom.LastEventID = string(evt.ID)
	matrixRoom.LastActivityAt = &now
	mbs.DB.Save(&matrixRoom)

	return nil
}

// isInWhitelist checks if a user is in the integration's whitelist
func (mbs *MatrixBotService) isInWhitelist(userID string, integration database.Integration) bool {
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return false
	}

	if whitelistInterface, exists := config["whitelist"]; exists {
		if whitelistArray, ok := whitelistInterface.([]interface{}); ok {
			if len(whitelistArray) == 0 {
				return false
			}

			for _, item := range whitelistArray {
				if str, ok := item.(string); ok && str == userID {
					return true
				}
			}
		}
	}

	return false
}

// SendTypingIndicator sends a typing indicator to a room
func (mbs *MatrixBotService) SendTypingIndicator(conn *MatrixClientConnection, roomID string, typing bool, timeout int64) error {
	_, err := conn.Client.UserTyping(conn.ctx, id.RoomID(roomID), typing, time.Duration(timeout)*time.Millisecond)
	return err
}

// MarkMessageAsRead marks a message as read
func (mbs *MatrixBotService) MarkMessageAsRead(conn *MatrixClientConnection, roomID string, eventID string) error {
	return conn.Client.MarkRead(conn.ctx, id.RoomID(roomID), id.EventID(eventID))
}
