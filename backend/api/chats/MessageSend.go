package chats

import (
	wsapi "backend/api/websocket"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"gorm.io/gorm"
)

// TODO: allow different message types
type SendMessage struct {
	Text        string                  `json:"text"`
	Reasoning   *[]string               `json:"reasoning,omitempty"`
	MetaData    *map[string]interface{} `json:"meta_data,omitempty"`
	ToolCalls   *[]interface{}          `json:"tool_calls,omitempty"`
	Attachments *[]FileAttachment       `json:"attachments,omitempty"`
}

type SendMessageWithReasoning struct {
	Text        string                  `json:"text"`
	Reasoning   []string                `json:"reasoning"`
	MetaData    *map[string]interface{} `json:"meta_data,omitempty"`
	ToolCalls   *[]interface{}          `json:"tool_calls,omitempty"`
	Attachments *[]FileAttachment       `json:"attachments,omitempty"`
}

type FileAttachment struct {
	FileID      string `json:"file_id"`
	DisplayName string `json:"display_name,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

// MessageData interface for different message types
type MessageData interface {
	GetText() string
	GetReasoning() []string
	GetMetaData() *map[string]interface{}
	GetToolCalls() *[]interface{}
	GetAttachments() *[]FileAttachment
}

// Add GetText and GetReasoning methods to both types
func (m SendMessage) GetText() string {
	return m.Text
}

func (m SendMessage) GetToolCalls() *[]interface{} {
	return m.ToolCalls
}

func (m SendMessage) GetReasoning() []string {
	if m.Reasoning == nil {
		return nil
	}
	return *m.Reasoning
}

func (m SendMessage) GetMetaData() *map[string]interface{} {
	return m.MetaData
}

func (m SendMessage) GetAttachments() *[]FileAttachment {
	return m.Attachments
}

func (m SendMessageWithReasoning) GetText() string {
	return m.Text
}

func (m SendMessageWithReasoning) GetReasoning() []string {
	return m.Reasoning
}

func (m SendMessageWithReasoning) GetMetaData() *map[string]interface{} {
	return m.MetaData
}

func (m SendMessageWithReasoning) GetToolCalls() *[]interface{} {
	return m.ToolCalls
}

func (m SendMessageWithReasoning) GetAttachments() *[]FileAttachment {
	return m.Attachments
}

// Send a message to a chat
//
//	@Summary      Send a message
//	@Description  Send a message to a chat
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Param        text body SendMessage true "Message content"
//	@Param        chat_uuid path string true "Chat UUID"
//	@Router       /api/v1/chats/{chat_uuid}/messages/send [post]
func (h *ChatsHandler) MessageSend(w http.ResponseWriter, r *http.Request) {
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

	var data SendMessage
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	chatUuid := r.PathValue("chat_uuid") // TODO - validate chat UUID!
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	// First find the chat by its id and user.ID
	var chat database.Chat
	result := DB.Preload("User1").
		Preload("User2").
		Preload("LatestMessage").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
	}

	var receiverId uint
	var receiver database.User
	if chat.User1.ID == user.ID {
		receiverId = chat.User2.ID
		receiver = chat.User2
	} else {
		receiverId = chat.User1.ID
		receiver = chat.User1
	}

	var message database.Message = database.Message{
		ChatId:     chat.ID,
		SenderId:   user.ID,
		ReceiverId: receiverId,
		Text:       &data.Text,
	}

	// Only set Reasoning if it's not nil
	if data.Reasoning != nil {
		message.Reasoning = data.Reasoning
	}

	// Only set MetaData if it's not nil
	if data.MetaData != nil {
		metadataBytes, err := json.Marshal(data.MetaData)
		if err == nil {
			message.MetaData = metadataBytes
		}
	}

	// Handle file attachments
	if data.Attachments != nil {
		// Validate that all attachments belong to the user and enrich with file details
		enrichedAttachments := make([]FileAttachment, len(*data.Attachments))
		for i, attachment := range *data.Attachments {
			var uploadedFile database.UploadedFile
			if err := DB.Where("file_id = ?", attachment.FileID).First(&uploadedFile).Error; err != nil {
				http.Error(w, "Invalid file attachment", http.StatusBadRequest)
				return
			}

			if uploadedFile.OwnerID != user.ID {
				http.Error(w, "Access denied to file attachment", http.StatusForbidden)
				return
			}

			// Share the file with the receiver
			var existingAccess database.FileAccess
			result := DB.Where("user_id = ? AND uploaded_file_id = ?", receiverId, uploadedFile.ID).First(&existingAccess)
			if result.Error != nil {
				// File access doesn't exist, create it
				fileAccess := database.FileAccess{
					UserID:         receiverId,
					UploadedFileID: uploadedFile.ID,
					Permission:     "view",
					CreatedAt:      time.Now(),
				}
				if err := DB.Create(&fileAccess).Error; err != nil {
					log.Printf("Error sharing file %s (ID: %d) with user %d: %v", attachment.FileID, uploadedFile.ID, receiverId, err)
					// Don't fail the message send if file sharing fails
				} else {
					log.Printf("Successfully shared file %s (ID: %d) with user %d", attachment.FileID, uploadedFile.ID, receiverId)
				}
			} else {
				log.Printf("File %s (ID: %d) already shared with user %d", attachment.FileID, uploadedFile.ID, receiverId)
			}

			// Enrich attachment with file details
			enrichedAttachments[i] = FileAttachment{
				FileID:      attachment.FileID,
				DisplayName: attachment.DisplayName,
				FileName:    uploadedFile.FileName,
				FileSize:    uploadedFile.Size,
				MimeType:    uploadedFile.MIMEType,
			}
		}

		// Store enriched attachments in metadata
		attachmentData := map[string]interface{}{
			"attachments": enrichedAttachments,
		}

		// Merge with existing metadata if any
		if message.MetaData != nil {
			var existingMeta map[string]interface{}
			if err := json.Unmarshal(message.MetaData, &existingMeta); err == nil {
				for k, v := range attachmentData {
					existingMeta[k] = v
				}
				attachmentData = existingMeta
			}
		}

		metadataBytes, err := json.Marshal(attachmentData)
		if err == nil {
			message.MetaData = metadataBytes
		}
	}

	if data.ToolCalls != nil {
		toolCalls := []json.RawMessage{}
		for _, toolCall := range *data.ToolCalls {
			toolCallBytes, err := json.Marshal(toolCall)
			if err == nil {
				toolCalls = append(toolCalls, toolCallBytes)
			}
		}
		message.ToolCalls = &toolCalls
	}

	// Wrap message creation and chat update in a transaction
	err = DB.Transaction(func(tx *gorm.DB) error {
		// Create the message
		if err := tx.Create(&message).Error; err != nil {
			return err
		}

		// Update only the latest_message_id field
		if err := tx.Model(&chat).Update("latest_message_id", message.ID).Error; err != nil {
			return err
		}

		// Reload the chat with the latest message for the response
		if err := tx.Preload("LatestMessage").First(&chat, chat.ID).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Now publish websocket updates to online & subscribed users
	SendWebsocketMessage(ch, receiver.UUID, chatUuid, *user, data)

	w.Header().Set("Content-Type", "application/json")

	// Convert the message to ListedMessage format to include metadata
	messageMetaData := map[string]interface{}{}
	if message.MetaData != nil {
		json.Unmarshal(message.MetaData, &messageMetaData)
	}

	toolCalls := []interface{}{}
	if message.ToolCalls != nil {
		for _, toolCall := range *message.ToolCalls {
			var toolCallData map[string]interface{}
			json.Unmarshal(toolCall, &toolCallData)
			toolCalls = append(toolCalls, toolCallData)
		}
	}

	listedMessage := ListedMessage{
		UUID:       message.UUID,
		SendAt:     message.CreatedAt.String(),
		SenderUUID: user.UUID,
		Text:       *message.Text,
		Reasoning:  message.Reasoning,
		ToolCalls:  &toolCalls,
		MetaData:   &messageMetaData,
	}

	json.NewEncoder(w).Encode(listedMessage)
}

func (h *ChatsHandler) SignalSendMessage(w http.ResponseWriter, r *http.Request) {
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

	chatUuid := r.PathValue("chat_uuid") // TODO - validate chat UUID!
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	signal := r.PathValue("signal")
	if signal == "" {
		http.Error(w, "Invalid signal", http.StatusBadRequest)
		return
	}

	var chat database.Chat
	result := DB.Preload("User1").
		Preload("User2").
		Preload("LatestMessage").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
	}

	var receiver database.User
	if chat.User1.ID == user.ID {
		receiver = chat.User2
	} else {
		receiver = chat.User1
	}

	if signal == "interrupt" {
		ch.MessageHandler.SendMessage(
			ch,
			receiver.UUID,
			ch.MessageHandler.InterruptSignal(
				chatUuid,
				user.UUID,
			),
		)
	} else {
		http.Error(w, "Invalid signal", http.StatusBadRequest)
		return
	}
}

func SendWebsocketMessage(ch *wsapi.WebSocketHandler, receiverId string, chatUuid string, user database.User, data MessageData) {
	// Convert attachments to websocket format
	var wsAttachments *[]wsapi.FileAttachment
	if data.GetAttachments() != nil {
		attachments := make([]wsapi.FileAttachment, len(*data.GetAttachments()))
		for i, att := range *data.GetAttachments() {
			attachments[i] = wsapi.FileAttachment{
				FileID:      att.FileID,
				DisplayName: att.DisplayName,
				FileName:    att.FileName,
				FileSize:    att.FileSize,
				MimeType:    att.MimeType,
			}
		}
		wsAttachments = &attachments
	}

	ch.MessageHandler.SendMessage(
		ch,
		receiverId,
		ch.MessageHandler.NewMessage(
			chatUuid,
			user.UUID,
			data.GetText(),
			data.GetReasoning(),
			data.GetMetaData(),
			data.GetToolCalls(),
			wsAttachments,
		),
	)
}
