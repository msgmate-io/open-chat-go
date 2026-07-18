package chats

import (
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

// TODO: should also supply user Id
type CreateChat struct {
	ContactToken string                 `json:"contact_token"`
	FirstMessage string                 `json:"first_message"`
	Attachments  []FileAttachment       `json:"attachments,omitempty"`
	SharedConfig map[string]interface{} `json:"shared_config,omitempty"`
	ChatType     string                 `json:"chat_type,omitempty"`
	AutoShare    bool                   `json:"auto_share,omitempty"`
}

func mergeJSONMaps(base map[string]interface{}, overrides map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		if baseMap, ok := out[k].(map[string]interface{}); ok {
			if overrideMap, ok := v.(map[string]interface{}); ok {
				out[k] = mergeJSONMaps(baseMap, overrideMap)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func applyModelConfigBindingForUser(DB *gorm.DB, ownerUserID uint, config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	if configuredBackend, _ := config["backend"].(string); strings.EqualFold(strings.TrimSpace(configuredBackend), "testbackend") {
		return config
	}
	if endpoint, _ := config["endpoint"].(string); strings.Contains(strings.ToLower(strings.TrimSpace(endpoint)), "testbackend.local") {
		return config
	}
	modelID, _ := config["model"].(string)
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return config
	}

	var modelCfg database.ModelConfig
	err := DB.Where("owner_user_id = ? AND model_id = ?", ownerUserID, modelID).Order("id desc").First(&modelCfg).Error
	if err != nil {
		err = DB.Where("owner_user_id IS NULL AND model_id = ?", modelID).Order("id desc").First(&modelCfg).Error
		if err != nil {
			return config
		}
	}

	cfgData := map[string]interface{}{}
	if len(modelCfg.Configuration) == 0 {
		return config
	}
	if err := json.Unmarshal(modelCfg.Configuration, &cfgData); err != nil {
		return config
	}
	if backend, ok := cfgData["backend"].(string); ok && backend != "" {
		config["backend"] = backend
	}
	if endpoint, ok := cfgData["endpoint"].(string); ok && endpoint != "" {
		config["endpoint"] = endpoint
	}
	return config
}

func resolveSharedConfigForChat(DB *gorm.DB, botUser database.User, requestSharedConfig map[string]interface{}) (map[string]interface{}, error) {
	if !botUser.IsAutomated {
		if requestSharedConfig == nil {
			return nil, nil
		}
		withDefaults := extiface.ApplySharedConfigDefaults(requestSharedConfig)
		return withDefaults, nil
	}
	var runtime database.BotRuntimeConfig
	if err := DB.Where("bot_user_id = ? AND is_active = ?", botUser.ID, true).Order("id desc").First(&runtime).Error; err != nil {
		if requestSharedConfig == nil {
			return nil, nil
		}
		withDefaults := extiface.ApplySharedConfigDefaults(requestSharedConfig)
		return withDefaults, nil
	}
	runtimeConfig := map[string]interface{}{}
	if len(runtime.DefaultSharedConfig) > 0 {
		_ = json.Unmarshal(runtime.DefaultSharedConfig, &runtimeConfig)
	}
	var resolved map[string]interface{}
	if requestSharedConfig == nil {
		if len(runtimeConfig) == 0 {
			return nil, nil
		}
		resolved = runtimeConfig
	} else if len(runtimeConfig) == 0 {
		resolved = applyModelConfigBindingForUser(DB, runtime.OwnerUserId, requestSharedConfig)
	} else {
		merged := mergeJSONMaps(runtimeConfig, requestSharedConfig)
		resolved = applyModelConfigBindingForUser(DB, runtime.OwnerUserId, merged)
	}
	withDefaults := extiface.ApplySharedConfigDefaults(resolved)
	return withDefaults, nil
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
	log.Printf("  AutoShare: %v", data.AutoShare)
	log.Printf("  SharedConfig keys: %d", len(data.SharedConfig))

	// Security check: Only allow known public chat types for non-admin users.
	// Admins may use custom chat types.
	if !user.IsAdmin {
		if data.ChatType != "" && data.ChatType != "conversation" && data.ChatType != "interaction" {
			data.ChatType = "conversation"
		}
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

	var createdMessage *database.Message

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
		createdMessage = &message
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

	resolvedSharedConfig, resolveSharedErr := resolveSharedConfigForChat(DB, otherUser, data.SharedConfig)
	if resolveSharedErr != nil {
		http.Error(w, "Failed to resolve shared_config", http.StatusInternalServerError)
		return
	}
	if resolvedSharedConfig != nil {
		configData, err := json.Marshal(resolvedSharedConfig)
		if err != nil {
			http.Error(w, "Invalid shared_config JSON", http.StatusBadRequest)
			return
		}
		sharedConfig := database.SharedChatConfig{
			ChatId:     chat.ID,
			ConfigData: configData,
		}
		DB.Create(&sharedConfig)
		chat.SharedConfigId = &sharedConfig.ID
		chat.SharedConfig = &sharedConfig
		DB.Save(&chat)
	}

	if data.FirstMessage != "" {
		if otherUser.IsAutomated && createdMessage != nil {
			queueClient, clientErr := util.GetAsynqClient(r)
			queueInspector, inspectorErr := util.GetAsynqInspector(r)
			if clientErr != nil || inspectorErr != nil {
				log.Printf("Failed to access async queue for initial bot reply: clientErr=%v inspectorErr=%v", clientErr, inspectorErr)
			} else {
				if _, enqueueErr := workqueue.EnqueueBotReply(queueClient, queueInspector, workqueue.BotReplyPayload{
					ChatUUID:    chat.UUID,
					MessageUUID: createdMessage.UUID,
					BotUserID:   otherUser.ID,
				}); enqueueErr != nil {
					log.Printf("Failed to enqueue initial bot reply for chat %s: %v", chat.UUID, enqueueErr)
				}
			}
		} else {
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
	}

	DB.Preload("User1").Preload("User2").Preload("LatestMessage").First(&chat, chat.ID)
	listedChat := convertChatToListedChat(user, chat)
	if data.AutoShare {
		share, shareErr := ensureOwnedChatShare(DB, chat, user.ID)
		if shareErr != nil {
			http.Error(w, "Failed to auto-share chat", http.StatusInternalServerError)
			return
		}
		listedChat.ChatShareUUID = share.ChatShareUUID
		baseURL := requestBaseURL(r)
		if baseURL != "" {
			listedChat.SharedChatURL = baseURL + "/interaction/" + share.ChatShareUUID
		}
	}

	log.Printf("=== ChatsHandler.Create END ===")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listedChat)

}
