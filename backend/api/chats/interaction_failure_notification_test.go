package chats

import (
	"context"
	"testing"

	"backend/database"
)

func TestIsInteractionFailureMetadata(t *testing.T) {
	errored := database.Message{DataType: "text", MetaData: database.JSONRaw(`{"finished":true,"error":true}`)}
	if !isInteractionFailureMetadata(errored, decodeOrFail(t, errored.MetaData)) {
		t.Fatalf("an errored message must count as a failure")
	}

	reasoned := database.Message{DataType: "text", MetaData: database.JSONRaw(`{"finished":true,"failure_reason":"provider_error"}`)}
	if !isInteractionFailureMetadata(reasoned, decodeOrFail(t, reasoned.MetaData)) {
		t.Fatalf("a failure_reason message must count as a failure")
	}

	completed := database.Message{DataType: "text", MetaData: database.JSONRaw(`{"finished":true}`)}
	if isInteractionFailureMetadata(completed, decodeOrFail(t, completed.MetaData)) {
		t.Fatalf("a clean completion must not count as a failure")
	}

	event := database.Message{DataType: "event", MetaData: database.JSONRaw(`{"finished":true,"error":true,"event_type":"provider_retry"}`)}
	if isInteractionFailureMetadata(event, decodeOrFail(t, event.MetaData)) {
		t.Fatalf("an in-flight event row must not count as a terminal failure")
	}
}

func TestInteractionFailureDetailPrecedence(t *testing.T) {
	if got := interactionFailureDetail(map[string]interface{}{"error_detail": "boom", "provider_error": "HTTP 402"}); got != "boom" {
		t.Fatalf("error_detail must win, got %q", got)
	}
	if got := interactionFailureDetail(map[string]interface{}{"provider_error": "HTTP 402"}); got != "HTTP 402" {
		t.Fatalf("provider_error fallback, got %q", got)
	}
	if got := interactionFailureDetail(map[string]interface{}{"failure_reason": "provider_error"}); got != "provider_error" {
		t.Fatalf("failure_reason fallback, got %q", got)
	}
	if got := interactionFailureDetail(map[string]interface{}{}); got != "" {
		t.Fatalf("empty metadata must yield empty detail, got %q", got)
	}
}

// TestMaybeDispatchInteractionFailureNotificationGuard pins the detection +
// caller contract: an errored terminal row is handled (true, so the caller
// skips the completion notification) and a clean completion is not. The
// slug selection / delivery is covered by the notifications package test.
func TestMaybeDispatchInteractionFailureNotificationGuard(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "failure-owner@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "failure-bot@example.com")

	chat := database.Chat{
		User1Id:  owner.ID,
		User2Id:  bot.ID,
		ChatType: "interaction",
		Tags:     database.StringSliceJSON{"repo:open-chat-go-ci"},
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	errored := database.Message{
		ChatId:   chat.ID,
		SenderId: bot.ID,
		MetaData: database.JSONRaw(`{"finished":true,"error":true,"error_detail":"HTTP 402 requires more credits"}`),
	}
	if !maybeDispatchInteractionFailureNotification(context.Background(), DB, chat, *bot, *owner, errored) {
		t.Fatalf("an errored terminal row must be handled by the failure notification path")
	}

	completed := database.Message{ChatId: chat.ID, SenderId: bot.ID, MetaData: database.JSONRaw(`{"finished":true}`)}
	if maybeDispatchInteractionFailureNotification(context.Background(), DB, chat, *bot, *owner, completed) {
		t.Fatalf("a clean completion must not be treated as a failure")
	}
}
