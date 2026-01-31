package msgmate

import (
	"testing"

	wsapi "backend/api/websocket"
	client "github.com/msgmate-io/go-client-integration/goclient"
)

// buildOpenAIMessages must not inject the display text of a non-conversational
// confirmable-action lifecycle event as a synthetic user turn. Doing so puts a
// user turn after the assistant tool-call + tool result, so a mock/replay
// provider that scans back for the tool result re-issues the confirmed tool call
// and loops (test_mocked_confirmable_tool_flow), and real models can re-issue
// instead of answering (ci#492/ci#493).
func TestBuildOpenAIMessages_ConfirmableActionEventDoesNotAppendUserTurn(t *testing.T) {
	handler := &AIHandlerImpl{}

	meta := map[string]interface{}{
		"finished":   true,
		"event_type": "confirmable_action_execute",
	}

	tests := []struct {
		name     string
		dataType string
		meta     *map[string]interface{}
	}{
		{name: "event data type", dataType: "event", meta: nil},
		{name: "confirmable_action_execute metadata", dataType: "", meta: &meta},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var message wsapi.NewMessage
			message.Content.Text = "Approved `get_current_time_confirmed_testing`; running it now."
			message.Content.DataType = tc.dataType
			message.Content.MetaData = tc.meta

			messages := handler.buildOpenAIMessages(&client.PaginatedMessages{}, message, "system prompt", "testbackend")

			for _, m := range messages {
				if role, _ := m["role"].(string); role == "user" {
					t.Fatalf("confirmable-action lifecycle event was appended as a user turn: %#v", m)
				}
			}
			if len(messages) != 1 {
				t.Fatalf("expected only the system prompt, got %d messages: %#v", len(messages), messages)
			}
		})
	}
}

// A genuine user message must still be appended when it is not already present
// in the history.
func TestBuildOpenAIMessages_NormalUserMessageStillAppended(t *testing.T) {
	handler := &AIHandlerImpl{}

	var message wsapi.NewMessage
	message.Content.Text = "What is the current time?"

	messages := handler.buildOpenAIMessages(&client.PaginatedMessages{}, message, "system prompt", "testbackend")

	appended := false
	for _, m := range messages {
		if role, _ := m["role"].(string); role == "user" {
			appended = true
			if content, _ := m["content"].(string); content != "What is the current time?" {
				t.Fatalf("unexpected user content: %#v", m["content"])
			}
		}
	}
	if !appended {
		t.Fatalf("normal user message was not appended: %#v", messages)
	}
}
