package drain

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"backend/database"
	"backend/workqueue"

	"github.com/glebarez/sqlite"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

func openDrainTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	DB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "terminal-drain.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	if err := DB.AutoMigrate(&database.Chat{}, &database.User{}, &database.Message{}); err != nil {
		t.Fatalf("failed to migrate test schema: %v", err)
	}
	return DB
}

// queuedTestMessage persists a message and marks it as queued, ie as waiting
// behind a live turn (ci#656).
func queuedTestMessage(t *testing.T, DB *gorm.DB) database.Message {
	t.Helper()
	chat := database.Chat{}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	message := database.Message{ChatId: chat.ID, DataType: "text"}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}
	if _, err := database.SetMessageQueued(DB, &message); err != nil {
		t.Fatalf("failed to mark the message queued: %v", err)
	}
	return message
}

func reloadTestMessage(t *testing.T, DB *gorm.DB, uuid string) database.Message {
	t.Helper()
	var persisted database.Message
	if err := DB.Where("uuid = ?", uuid).First(&persisted).Error; err != nil {
		t.Fatalf("failed to reload message %s: %v", uuid, err)
	}
	return persisted
}

func botReplyTestTask(t *testing.T, messageUUID string) *asynq.Task {
	t.Helper()
	task, err := workqueue.NewBotReplyTask(workqueue.BotReplyPayload{
		ChatUUID:    "chat-1",
		MessageUUID: messageUUID,
		BotUserID:   7,
	})
	if err != nil {
		t.Fatalf("failed to build the bot reply task: %v", err)
	}
	return task
}

func TestBotReplyFailureTerminal(t *testing.T) {
	tests := []struct {
		name    string
		failure BotReplyFailure
		want    bool
	}{
		{name: "nil error", failure: BotReplyFailure{}, want: false},
		{name: "skip retry", failure: BotReplyFailure{Err: asynq.SkipRetry}, want: true},
		{name: "wrapped skip retry", failure: BotReplyFailure{Err: fmt.Errorf("turn interrupted: %w", asynq.SkipRetry)}, want: true},
		{name: "revoked task", failure: BotReplyFailure{Err: asynq.RevokeTask}, want: true},
		{name: "plain error without retry metadata", failure: BotReplyFailure{Err: errors.New("boom")}, want: false},
		{name: "retries left", failure: BotReplyFailure{Err: errors.New("boom"), RetryCount: 3, MaxRetry: 10, RetryKnown: true}, want: false},
		{name: "retries exhausted", failure: BotReplyFailure{Err: errors.New("boom"), RetryCount: 10, MaxRetry: 10, RetryKnown: true}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.failure.Terminal(); got != tc.want {
				t.Fatalf("Terminal() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBotReplyTerminalDrainDropsTheMarkerOfARetiredReply(t *testing.T) {
	DB := openDrainTestDB(t)
	message := queuedTestMessage(t, DB)
	if !database.IsMessageQueued(message) {
		t.Fatalf("the test message must start out queued")
	}

	BotReplyTerminalDrain(DB).HandleError(
		context.Background(),
		botReplyTestTask(t, message.UUID),
		fmt.Errorf("bot reply generation failed: %w", asynq.SkipRetry),
	)

	if database.IsMessageQueued(reloadTestMessage(t, DB, message.UUID)) {
		t.Fatalf("a retired reply must not leave its message rendering as queued")
	}
}

func TestBotReplyTerminalDrainKeepsTheMarkerOfARetryableFailure(t *testing.T) {
	DB := openDrainTestDB(t)
	message := queuedTestMessage(t, DB)

	BotReplyTerminalDrain(DB).HandleError(
		context.Background(),
		botReplyTestTask(t, message.UUID),
		errors.New("dial tcp 127.0.0.1:443: connection refused"),
	)

	if !database.IsMessageQueued(reloadTestMessage(t, DB, message.UUID)) {
		t.Fatalf("a retryable failure must keep the marker: the reply can still be consumed")
	}
}

func TestBotReplyTerminalDrainLeavesOtherMessagesAlone(t *testing.T) {
	DB := openDrainTestDB(t)
	// A newer reply queued behind the same chat must keep its own marker.
	failing := queuedTestMessage(t, DB)
	other := queuedTestMessage(t, DB)

	BotReplyTerminalDrain(DB).HandleError(
		context.Background(),
		botReplyTestTask(t, failing.UUID),
		asynq.SkipRetry,
	)

	if database.IsMessageQueued(reloadTestMessage(t, DB, failing.UUID)) {
		t.Fatalf("the retired reply's message must be drained")
	}
	if !database.IsMessageQueued(reloadTestMessage(t, DB, other.UUID)) {
		t.Fatalf("another queued message must not be touched")
	}
}

func TestBotReplyTerminalDrainIgnoresOtherTaskTypes(t *testing.T) {
	DB := openDrainTestDB(t)
	message := queuedTestMessage(t, DB)

	BotReplyTerminalDrain(DB).HandleError(context.Background(), asynq.NewTask("email:automation", []byte("{}")), asynq.SkipRetry)

	if !database.IsMessageQueued(reloadTestMessage(t, DB, message.UUID)) {
		t.Fatalf("a non bot-reply task must never touch a queued marker")
	}
}

func TestBotReplyTerminalDrainToleratesMissingInputs(t *testing.T) {
	BotReplyTerminalDrain(nil).HandleError(context.Background(), nil, asynq.SkipRetry)

	DB := openDrainTestDB(t)
	BotReplyTerminalDrain(DB).HandleError(context.Background(), nil, asynq.SkipRetry)

	// A retired reply whose source message is already gone must not fail.
	BotReplyTerminalDrain(DB).HandleError(
		context.Background(),
		botReplyTestTask(t, "missing-message-uuid"),
		asynq.SkipRetry,
	)

	// An undecodable payload must be tolerated too.
	BotReplyTerminalDrain(DB).HandleError(
		context.Background(),
		asynq.NewTask(workqueue.TypeBotReply, []byte("not json")),
		asynq.SkipRetry,
	)
}
