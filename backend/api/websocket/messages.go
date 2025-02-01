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
		ChatUUID    string                  `json:"chat_uuid"`
		SenderUUID  string                  `json:"sender_uuid"`
		Text        string                  `json:"text"`
		Reasoning   []string                `json:"reasoning"`
		MetaData    *map[string]interface{} `json:"meta_data,omitempty"`
		ToolCalls   *[]interface{}          `json:"tool_calls,omitempty"`
		Attachments *[]FileAttachment       `json:"attachments,omitempty"`
	} `json:"content"`
}

type NewPartialMessage struct {
	Type    string `json:"type"`
	Content struct {
		ChatUUID    string                  `json:"chat_uuid"`
		SenderUUID  string                  `json:"sender_uuid"`
		Text        string                  `json:"text"`
		Reasoning   []string                `json:"reasoning"`
		MetaData    *map[string]interface{} `json:"meta_data,omitempty"`
		ToolCalls   *[]interface{}          `json:"tool_calls,omitempty"`
		Attachments *[]FileAttachment       `json:"attachments,omitempty"`
	} `json:"content"`
}

type StartPartialMessage struct {
	Type    string `json:"type"`
	Content struct {
		ChatUUID   string `json:"chat_uuid"`
		SenderUUID string `json:"sender_uuid"`
	} `json:"content"`
}

type EndPartialMessage struct {
	Type    string `json:"type"`
	Content struct {
		ChatUUID   string `json:"chat_uuid"`
		SenderUUID string `json:"sender_uuid"`
	} `json:"content"`
}

type InterruptSignal struct {
	Type    string `json:"type"`
	Content struct {
		ChatUUID   string `json:"chat_uuid"`
		SenderUUID string `json:"sender_uuid"`
	} `json:"content"`
}

type FileAttachment struct {
	FileID      string `json:"file_id"`
	DisplayName string `json:"display_name,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

func (m *Messages) SendMessage(ch *WebSocketHandler, receiverUUID string, EncMessage []byte) {
	ch.PublishInChannel(
		EncMessage,
		receiverUUID,
	)
}

func (m *Messages) StartPartialMessage(ChatUUID, SenderUUID string) []byte {
	msg := StartPartialMessage{
		Type: "start_partial_message",
		Content: struct {
			ChatUUID   string `json:"chat_uuid"`
			SenderUUID string `json:"sender_uuid"`
		}{
			ChatUUID:   ChatUUID,
			SenderUUID: SenderUUID,
		},
	}

	encMsg, _ := json.Marshal(msg)
	return encMsg
}

func (m *Messages) EndPartialMessage(ChatUUID, SenderUUID string) []byte {
	msg := EndPartialMessage{
		Type: "end_partial_message",
		Content: struct {
			ChatUUID   string `json:"chat_uuid"`
			SenderUUID string `json:"sender_uuid"`
		}{
			ChatUUID:   ChatUUID,
			SenderUUID: SenderUUID,
		},
	}

	encMsg, _ := json.Marshal(msg)
	return encMsg
}

func (m *Messages) NewPartialMessage(ChatUUID, SenderUUID, Text string, Reasoning []string, MetaData *map[string]interface{}, ToolCalls *[]interface{}, Attachments *[]FileAttachment) []byte {
	msg := NewPartialMessage{
		Type: "new_partial_message",
		Content: struct {
			ChatUUID    string                  `json:"chat_uuid"`
			SenderUUID  string                  `json:"sender_uuid"`
			Text        string                  `json:"text"`
			Reasoning   []string                `json:"reasoning"`
			MetaData    *map[string]interface{} `json:"meta_data,omitempty"`
			ToolCalls   *[]interface{}          `json:"tool_calls,omitempty"`
			Attachments *[]FileAttachment       `json:"attachments,omitempty"`
		}{
			ChatUUID:    ChatUUID,
			SenderUUID:  SenderUUID,
			Text:        Text,
			Reasoning:   Reasoning,
			MetaData:    MetaData,
			ToolCalls:   ToolCalls,
			Attachments: Attachments,
		},
	}

	encMsg, _ := json.Marshal(msg)
	return encMsg
}

func (m *Messages) NewMessage(ChatUUID, SenderUUID, Text string, Reasoning []string, MetaData *map[string]interface{}, ToolCalls *[]interface{}, Attachments *[]FileAttachment) []byte {
	msg := NewMessage{
		Type: "new_message",
		Content: struct {
			ChatUUID    string                  `json:"chat_uuid"`
			SenderUUID  string                  `json:"sender_uuid"`
			Text        string                  `json:"text"`
			Reasoning   []string                `json:"reasoning"`
			MetaData    *map[string]interface{} `json:"meta_data,omitempty"`
			ToolCalls   *[]interface{}          `json:"tool_calls,omitempty"`
			Attachments *[]FileAttachment       `json:"attachments,omitempty"`
		}{
			ChatUUID:    ChatUUID,
			SenderUUID:  SenderUUID,
			Text:        Text,
			Reasoning:   Reasoning,
			MetaData:    MetaData,
			ToolCalls:   ToolCalls,
			Attachments: Attachments,
		},
	}

	encMsg, _ := json.Marshal(msg)
	return encMsg
}

func (m *Messages) InterruptSignal(ChatUUID string, SenderUUID string) []byte {
	msg := InterruptSignal{
		Type: "interrupt_signal",
		Content: struct {
			ChatUUID   string `json:"chat_uuid"`
			SenderUUID string `json:"sender_uuid"`
		}{
			ChatUUID:   ChatUUID,
			SenderUUID: SenderUUID,
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
