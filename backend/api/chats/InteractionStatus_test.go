package chats

import (
	"testing"

	"backend/database"
)

func TestMessageHasPendingConfirmationRecognizesOpencodeNeedsAction(t *testing.T) {
	pending := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"opencode_needs_action":{"status":"pending","reason":"ApiError","message":"invalid api key"}}`),
	}
	if !messageHasPendingConfirmation(pending) {
		t.Fatalf("expected a pending opencode_needs_action marker to count as a pending confirmation")
	}

	resolved := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"opencode_needs_action":{"status":"resolved"}}`),
	}
	if messageHasPendingConfirmation(resolved) {
		t.Fatalf("expected a resolved opencode_needs_action marker to be ignored")
	}

	absent := database.Message{
		MetaData: database.JSONRaw(`{"finished":true}`),
	}
	if messageHasPendingConfirmation(absent) {
		t.Fatalf("expected a message without markers to be ignored")
	}
}
