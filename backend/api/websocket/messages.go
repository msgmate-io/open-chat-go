package websocket

import (
	"encoding/json"
)

type Messages struct{}

type UserWentOnline struct {
	Type    string `json:"type"`
	Content struct {
		UserUUID string `json:"user_uuid"`
	} `json:"content"`
}

type NewMessage struct {
	Type    string `json:"type"`
	Content struct {
		ChatUUID   string `json:"chat_uuid"`
		SenderUUID string `json:"sender_uuid"`
		Text       string `json:"text"`
	} `json:"content"`
}

type NewPartialMessage struct {
	Type    string `json:"type"`
	Content struct {
		ChatUUID   string `json:"chat_uuid"`
		SenderUUID string `json:"sender_uuid"`
		Text       string `json:"text"`
	} `json:"content"`
}

func (m *Messages) SendMessage(ch *WebSocketHandler, receiverUUID string, EncMessage []byte) {
	ch.PublishInChannel(
		EncMessage,
		receiverUUID,
	)
}

func (m *Messages) NewPartialMessage(ChatUUID, SenderUUID, Text string) []byte {
	msg := NewPartialMessage{
		Type: "new_partial_message",
		Content: struct {
			ChatUUID   string `json:"chat_uuid"`
			SenderUUID string `json:"sender_uuid"`
			Text       string `json:"text"`
		}{
			ChatUUID:   ChatUUID,
			SenderUUID: SenderUUID,
			Text:       Text,
		},
	}

	encMsg, _ := json.Marshal(msg)
	return encMsg
}

func (m *Messages) NewMessage(ChatUUID, SenderUUID, Text string) []byte {
	msg := NewMessage{
		Type: "new_message",
		Content: struct {
			ChatUUID   string `json:"chat_uuid"`
			SenderUUID string `json:"sender_uuid"`
			Text       string `json:"text"`
		}{
			ChatUUID:   ChatUUID,
			SenderUUID: SenderUUID,
			Text:       Text,
		},
	}

	encMsg, _ := json.Marshal(msg)
	return encMsg
}

func (m *Messages) UserWentOnline(UserUUID string) []byte {
	msg := UserWentOnline{
		Type: "user_went_online",
		Content: struct {
			UserUUID string `json:"user_uuid"`
		}{
			UserUUID: UserUUID,
		},
	}

	encMsg, _ := json.Marshal(msg)
	return encMsg
}
