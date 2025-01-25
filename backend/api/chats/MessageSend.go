package chats

import (
	"backend/api/websocket"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"

	"gorm.io/gorm"
)

// TODO: allow different message types
type SendMessage struct {
	Text      string                  `json:"text"`
	Reasoning *[]string               `json:"reasoning,omitempty"`
	MetaData  *map[string]interface{} `json:"meta_data,omitempty"`
	ToolCalls *[]interface{}          `json:"tool_calls,omitempty"`
}

type SendMessageWithReasoning struct {
	Text      string                  `json:"text"`
	Reasoning []string                `json:"reasoning"`
	MetaData  *map[string]interface{} `json:"meta_data,omitempty"`
	ToolCalls *[]interface{}          `json:"tool_calls,omitempty"`
}

// MessageData interface for different message types
type MessageData interface {
	GetText() string
	GetReasoning() []string
	GetMetaData() *map[string]interface{}
	GetToolCalls() *[]interface{}
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

		// Update both the latest_message_id and the LatestMessage association
		if err := tx.Model(&chat).Updates(map[string]interface{}{
			"latest_message_id": message.ID,
			"LatestMessage":     message,
		}).Error; err != nil {
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
	json.NewEncoder(w).Encode(ListedMessage{
		UUID:       message.UUID,
		SendAt:     message.CreatedAt.String(),
		SenderUUID: user.UUID,
		Text:       *message.Text,
	})
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

func SendWebsocketMessage(ch *websocket.WebSocketHandler, receiverId string, chatUuid string, user database.User, data MessageData) {
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
		),
	)
}
