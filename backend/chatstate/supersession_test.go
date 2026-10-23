package chatstate

import "testing"

// TestSupersessionMarkerLifecycle guards the chat-scoped supersession signal
// (ci#642): mark once, consume once, and never leak a marker to a later turn.
func TestSupersessionMarkerLifecycle(t *testing.T) {
	const chat = "chat-supersede-1"
	ClearChatSuperseded(chat)

	if ConsumeChatSuperseded(chat) {
		t.Fatal("consume before mark must be false")
	}

	MarkChatSuperseded(chat)
	if !ConsumeChatSuperseded(chat) {
		t.Fatal("a marked chat must report superseded")
	}
	if ConsumeChatSuperseded(chat) {
		t.Fatal("the marker must be consumed exactly once")
	}

	MarkChatSuperseded(chat)
	ClearChatSuperseded(chat)
	if ConsumeChatSuperseded(chat) {
		t.Fatal("cleared marker must not be consumed")
	}
}

func TestSupersessionMarkerIgnoresEmptyChat(t *testing.T) {
	MarkChatSuperseded("")
	if ConsumeChatSuperseded("") {
		t.Fatal("empty chat uuid must never report superseded")
	}
}
