package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// TODO: should also supply user Id
type CreateChat struct {
	ContactToken string           `json:"contact_token"`
	FirstMessage string           `json:"first_message"`
	Attachments  []FileAttachment `json:"attachments,omitempty"`
	SharedConfig json.RawMessage  `json:"shared_config"`
	ChatType     string           `json:"chat_type,omitempty"`
}

// Create a chat
//
//	@Summary      Create a chat
//	@Description  Create a new chat with another user, optionally including a first message and attachments
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        request body CreateChat true "Chat creation request"
//	@Success      200  {object}  ListedChat	"Chat created successfully"
//	@Failure      400  {string}  string	"Invalid request - bad JSON, missing contact token, or invalid contact token" Example("Invalid contact token")
//	@Failure      500  {string}  string	"Internal server error - failed to marshal attachment metadata" Example("Failed to marshal attachment metadata")
//	@Router       /api/v1/chats/create [post]
func (h *ChatsHandler) Create(w http.ResponseWriter, r *http.Request) {
	log.Printf("=== ChatsHandler.Create START ===")

	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	ch, err := util.GetWebsocket(r)
	if err != nil {
		http.Error(w, "Unable to get websocket", http.StatusBadRequest)
		return
	}

	var data CreateChat
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("Received CreateChat data:")
	log.Printf("  ContactToken: %s", data.ContactToken)
	log.Printf("  FirstMessage: %s", data.FirstMessage)
	log.Printf("  Attachments: %+v", data.Attachments)
	log.Printf("  ChatType: %s", data.ChatType)
	log.Printf("  SharedConfig length: %d", len(data.SharedConfig))

	// Security check: Only allow custom chat types for admin users
	// For regular users, force "conversation" type
	if data.ChatType != "" && data.ChatType != "conversation" && !user.IsAdmin {
		data.ChatType = "conversation" // Override with default for non-admin users
	}

	// If no chat type is specified, use the default
	if data.ChatType == "" {
		data.ChatType = "conversation"
	}

	var otherUser database.User
	if err := DB.First(&otherUser, "contact_token = ?", data.ContactToken).Error; err != nil {
		http.Error(w, "Invalid contact token", http.StatusBadRequest)
		return
	}

	// TODO check for blocked users
	// Small optimization, try to always ensure User1Id < User2Id
	var chat database.Chat
	if user.ID < otherUser.ID {
		chat = database.Chat{
			User1Id:  user.ID,
			User2Id:  otherUser.ID,
			ChatType: data.ChatType,
		}
	} else {
		chat = database.Chat{
			User1Id:  otherUser.ID,
			User2Id:  user.ID,
			ChatType: data.ChatType,
		}
	}

	DB.Create(&chat)
	DB.Preload("User1").Preload("User2").Preload("LatestMessage").First(&chat, chat.ID)

	if data.FirstMessage != "" {
		// Prepare metadata for attachments if any
		var metaData []byte
		if len(data.Attachments) > 0 {
			log.Printf("Processing %d attachments for first message", len(data.Attachments))
			attachmentsData := make([]map[string]interface{}, len(data.Attachments))
			for i, attachment := range data.Attachments {
				log.Printf("Processing attachment %d: %+v", i, attachment)
				attachmentsData[i] = map[string]interface{}{
					"file_id": attachment.FileID,
				}
			}

			metaDataMap := map[string]interface{}{
				"attachments": attachmentsData,
			}

			metaData, err = json.Marshal(metaDataMap)
			if err != nil {
				log.Printf("Failed to marshal attachment metadata: %v", err)
				http.Error(w, "Failed to marshal attachment metadata", http.StatusInternalServerError)
				return
			}
			log.Printf("Created metadata for attachments: %s", string(metaData))
		}

		message := database.Message{
			ChatId:     chat.ID,
			SenderId:   user.ID,
			ReceiverId: otherUser.ID,
			Text:       &data.FirstMessage,
			MetaData:   metaData,
		}
		DB.Create(&message)
		chat.LatestMessageId = &message.ID
		DB.Save(&chat)

		// If this is an AI interaction chat with attachments, share files with the bot user
		if data.ChatType == "interaction" && len(data.Attachments) > 0 {
			log.Printf("Sharing %d attachments with bot user for AI interaction", len(data.Attachments))

			for _, attachment := range data.Attachments {
				// Get the file record
				var uploadedFile database.UploadedFile
				if err := DB.Where("file_id = ?", attachment.FileID).First(&uploadedFile).Error; err != nil {
					log.Printf("Warning: File %s not found for sharing with bot user", attachment.FileID)
					continue
				}

				// Check if file is already shared with the bot user
				var existingAccess database.FileAccess
				result := DB.Where("user_id = ? AND uploaded_file_id = ?", otherUser.ID, uploadedFile.ID).First(&existingAccess)
				if result.Error != nil {
					// File access doesn't exist, create it
					fileAccess := database.FileAccess{
						UserID:         otherUser.ID,
						UploadedFileID: uploadedFile.ID,
						Permission:     "view",
						CreatedAt:      time.Now(),
					}
					if err := DB.Create(&fileAccess).Error; err != nil {
						log.Printf("Error sharing file %s (ID: %d) with bot user %d: %v", attachment.FileID, uploadedFile.ID, otherUser.ID, err)
						// Don't fail the chat creation if file sharing fails
					} else {
						log.Printf("Successfully shared file %s (ID: %d) with bot user %d for AI interaction", attachment.FileID, uploadedFile.ID, otherUser.ID)
					}
				} else {
					log.Printf("File %s (ID: %d) already shared with bot user %d", attachment.FileID, uploadedFile.ID, otherUser.ID)
				}
			}
		}
	}

	if data.SharedConfig != nil {
		sharedConfig := database.SharedChatConfig{
			ChatId:     chat.ID,
			ConfigData: data.SharedConfig,
		}
		DB.Create(&sharedConfig)
		chat.SharedConfigId = &sharedConfig.ID
		chat.SharedConfig = &sharedConfig
		DB.Save(&chat)
	}

	if data.FirstMessage != "" {
		// Prepare attachments for websocket message
		var wsAttachments *[]FileAttachment
		if len(data.Attachments) > 0 {
			wsAttachments = &data.Attachments
			log.Printf("Sending websocket message with %d attachments", len(data.Attachments))
		}

		SendWebsocketMessage(ch, otherUser.UUID, chat.UUID, *user, SendMessage{
			Text:        data.FirstMessage,
			Attachments: wsAttachments,
		})
	}

	DB.Preload("User1").Preload("User2").Preload("LatestMessage").First(&chat, chat.ID)
	listedChat := convertChatToListedChat(user, chat)

	log.Printf("=== ChatsHandler.Create END ===")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listedChat)

}
