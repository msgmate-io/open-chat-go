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

func TestMessageHasPendingConfirmationRecognizesInteractionConfirmation(t *testing.T) {
	pending := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"interaction_confirmation":{"status":"pending","source_message_uuid":"msg-1"}}`),
	}
	if !messageHasPendingConfirmation(pending) {
		t.Fatalf("expected a pending interaction_confirmation to count as a pending confirmation")
	}

	approved := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"interaction_confirmation":{"status":"approved"}}`),
	}
	if messageHasPendingConfirmation(approved) {
		t.Fatalf("expected an approved interaction_confirmation to be ignored")
	}

	rejected := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"interaction_confirmation":{"status":"rejected"}}`),
	}
	if messageHasPendingConfirmation(rejected) {
		t.Fatalf("expected a rejected interaction_confirmation to be ignored")
	}
}
