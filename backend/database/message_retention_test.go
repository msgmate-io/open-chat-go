package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

func setupMessageRetentionDB(t *testing.T) *gorm.DB {
	t.Helper()
	DB := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "message_retention_test.db"),
		ResetDB:  true,
	})
	if DB == nil {
		t.Fatal("failed to set up test database")
	}
	return DB
}

// createRetentionTestUser keeps the retention fixtures independent of the auth
// machinery: the prune only cares about message rows and their chat_id.
func createRetentionTestUser(t *testing.T, DB *gorm.DB, username string) *User {
	t.Helper()
	user := User{Username: username, Email: username, ContactToken: username + "-token"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user %q: %v", username, err)
	}
	return &user
}

// createRetentionTestChat creates a conversation between two fresh users.
func createRetentionTestChat(t *testing.T, DB *gorm.DB, suffix string) Chat {
	t.Helper()
	user1 := createRetentionTestUser(t, DB, "retention-owner-"+suffix+"@example.com")
	user2 := createRetentionTestUser(t, DB, "retention-partner-"+suffix+"@example.com")

	chat := Chat{User1Id: user1.ID, User2Id: user2.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	return chat
}

// seedRetentionMessages appends count messages to the chat, all created at
// createdAt (the prune keys off created_at, not id).
func seedRetentionMessages(t *testing.T, DB *gorm.DB, chat Chat, count int, createdAt time.Time) []Message {
	t.Helper()
	text := "hello"
	body := []byte("hello")
	messages := make([]Message, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, Message{
			ChatId:     chat.ID,
			SenderId:   chat.User1Id,
			ReceiverId: chat.User2Id,
			DataType:   "text",
			Text:       &text,
			Content:    &body,
		})
	}
	if err := DB.Create(&messages).Error; err != nil {
		t.Fatalf("failed to seed messages: %v", err)
	}

	// CreatedAt is promoted from Model, so it cannot be set in the composite
	// literal above; stamp it afterwards (UpdateColumn skips the autoUpdateTime
	// hook). The prune keys off created_at, not id.
	ids := make([]uint, len(messages))
	for i := range messages {
		ids[i] = messages[i].ID
	}
	if err := DB.Model(&Message{}).Where("id IN ?", ids).UpdateColumn("created_at", createdAt).Error; err != nil {
		t.Fatalf("failed to backdate seeded messages: %v", err)
	}
	return messages
}

// countRetentionMessages reports the rows still present. It queries unscoped on
// purpose: a soft delete would leave the row (and its bytes) behind, so a
// stamped row must count as a failure, not as a success.
func countRetentionMessages(t *testing.T, DB *gorm.DB, chat Chat) int64 {
	t.Helper()
	var count int64
	if err := DB.Unscoped().Model(&Message{}).Where("chat_id = ?", chat.ID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count messages: %v", err)
	}
	return count
}

// TestMessageRetentionPolicyWithDefaults keeps the non-negotiables of a
// partially specified policy: a zero floor/batch is filled in so a caller
// cannot accidentally prune every message of a chat or delete an unbounded
// number of rows in one transaction. A zero window stays zero (disabled).
func TestMessageRetentionPolicyWithDefaults(t *testing.T) {
	filled := MessageRetentionPolicy{Window: time.Hour}.withDefaults()
	if filled.HotFloorPerChat != HotMessageFloorPerChat {
		t.Fatalf("zero floor = %d, want the default %d", filled.HotFloorPerChat, HotMessageFloorPerChat)
	}
	if filled.BatchSize != DefaultMessagePruneBatchSize {
		t.Fatalf("zero batch size = %d, want the default %d", filled.BatchSize, DefaultMessagePruneBatchSize)
	}
	if filled.Window != time.Hour {
		t.Fatalf("withDefaults must not change an explicit window, got %s", filled.Window)
	}
	if disabled := (MessageRetentionPolicy{}).withDefaults(); disabled.Window != 0 {
		t.Fatalf("a zero window must stay disabled, got %s", disabled.Window)
	}
}

// TestDefaultMessageRetentionPolicyMatchesServerConfig pins the policy the
// server actually runs to the resolved environment knobs.
func TestDefaultMessageRetentionPolicyMatchesServerConfig(t *testing.T) {
	policy := DefaultMessageRetentionPolicy()
	if policy.Window != MessageRetentionWindow() {
		t.Fatalf("policy window = %s, want %s", policy.Window, MessageRetentionWindow())
	}
	if policy.HotFloorPerChat != HotMessageFloorPerChat {
		t.Fatalf("policy floor = %d, want %d", policy.HotFloorPerChat, HotMessageFloorPerChat)
	}
	if policy.BatchSize != MessagePruneBatchSize() {
		t.Fatalf("policy batch size = %d, want %d", policy.BatchSize, MessagePruneBatchSize())
	}
}

// TestMessageRetentionEnvKnobs pins the environment knobs: unset and unparsable
// values fall back to the defaults, the documented off-switches disable the
// prune, and an explicit value wins.
func TestMessageRetentionEnvKnobs(t *testing.T) {
	t.Run("window", func(t *testing.T) {
		if got := MessageRetentionWindow(); got != DefaultMessageRetentionWindow {
			t.Fatalf("unset window = %s, want the default %s", got, DefaultMessageRetentionWindow)
		}
		t.Setenv(MessageRetentionWindowEnv, "48h")
		if got := MessageRetentionWindow(); got != 48*time.Hour {
			t.Fatalf("window = %s, want 48h", got)
		}
		t.Setenv(MessageRetentionWindowEnv, "not-a-duration")
		if got := MessageRetentionWindow(); got != DefaultMessageRetentionWindow {
			t.Fatalf("unparsable window = %s, want the default %s", got, DefaultMessageRetentionWindow)
		}
		t.Setenv(MessageRetentionWindowEnv, "off")
		if got := MessageRetentionWindow(); got != 0 {
			t.Fatalf("`off` window = %s, want 0 (disabled)", got)
		}
		t.Setenv(MessageRetentionWindowEnv, "0")
		if got := MessageRetentionWindow(); got != 0 {
			t.Fatalf("`0` window = %s, want 0 (disabled)", got)
		}
	})

	t.Run("interval", func(t *testing.T) {
		if got := MessageRetentionInterval(); got != DefaultMessageRetentionInterval {
			t.Fatalf("unset interval = %s, want the default %s", got, DefaultMessageRetentionInterval)
		}
		t.Setenv(MessageRetentionIntervalEnv, "30m")
		if got := MessageRetentionInterval(); got != 30*time.Minute {
			t.Fatalf("interval = %s, want 30m", got)
		}
		t.Setenv(MessageRetentionIntervalEnv, "never")
		if got := MessageRetentionInterval(); got != 0 {
			t.Fatalf("`never` interval = %s, want 0 (disabled)", got)
		}
	})

	t.Run("batch size", func(t *testing.T) {
		if got := MessagePruneBatchSize(); got != DefaultMessagePruneBatchSize {
			t.Fatalf("unset batch size = %d, want the default %d", got, DefaultMessagePruneBatchSize)
		}
		t.Setenv(MessagePruneBatchSizeEnv, "25")
		if got := MessagePruneBatchSize(); got != 25 {
			t.Fatalf("batch size = %d, want 25", got)
		}
		for _, raw := range []string{"0", "-3", "lots"} {
			t.Setenv(MessagePruneBatchSizeEnv, raw)
			if got := MessagePruneBatchSize(); got != DefaultMessagePruneBatchSize {
				t.Fatalf("batch size %q = %d, want the default %d", raw, got, DefaultMessagePruneBatchSize)
			}
		}
	})
}

// TestDeleteExpiredMessagesKeepsHotFloorPerChat is the core retention contract:
// old messages are pruned, the newest HotFloorPerChat of every chat survive
// however old they are, and a recent message is never a candidate.
func TestDeleteExpiredMessagesKeepsHotFloorPerChat(t *testing.T) {
	DB := setupMessageRetentionDB(t)
	now := time.Now().UTC()

	oldChat := createRetentionTestChat(t, DB, "old")
	seedRetentionMessages(t, DB, oldChat, 10, now.Add(-100*24*time.Hour))
	freshChat := createRetentionTestChat(t, DB, "fresh")
	seedRetentionMessages(t, DB, freshChat, 10, now.Add(-time.Hour))

	policy := MessageRetentionPolicy{Window: 24 * time.Hour, HotFloorPerChat: 3, BatchSize: 100}
	deleted, err := DeleteExpiredMessages(context.Background(), DB, now, policy)
	if err != nil {
		t.Fatalf("retention prune failed: %v", err)
	}
	if deleted != 7 {
		t.Fatalf("deleted %d message(s), want 7 (10 old minus a floor of 3)", deleted)
	}
	if remaining := countRetentionMessages(t, DB, oldChat); remaining != 3 {
		t.Fatalf("old chat has %d rows left, want the 3-message hot floor", remaining)
	}
	if remaining := countRetentionMessages(t, DB, freshChat); remaining != 10 {
		t.Fatalf("recent chat lost rows: %d left, want 10", remaining)
	}

	// The kept rows must be the newest ones, so `chats.latest_message_id`
	// still resolves and the chat list keeps its latest message.
	var newest Message
	if err := DB.Where("chat_id = ?", oldChat.ID).Order("id DESC").First(&newest).Error; err != nil {
		t.Fatalf("the chat lost its newest message: %v", err)
	}

	// A second pass must not find anything: the floor is per chat and stable.
	again, err := DeleteExpiredMessages(context.Background(), DB, now, policy)
	if err != nil {
		t.Fatalf("second retention prune failed: %v", err)
	}
	if again != 0 {
		t.Fatalf("a repeated prune deleted %d row(s), want 0", again)
	}
}

// TestDeleteExpiredMessagesBatches pins the incremental reclaim: each
// transaction deletes at most BatchSize rows and the caller loops, so a large
// backlog never holds the SQLite write lock for one long write.
func TestDeleteExpiredMessagesBatches(t *testing.T) {
	DB := setupMessageRetentionDB(t)
	now := time.Now().UTC()

	chat := createRetentionTestChat(t, DB, "batched")
	seedRetentionMessages(t, DB, chat, 9, now.Add(-100*24*time.Hour))

	policy := MessageRetentionPolicy{Window: 24 * time.Hour, HotFloorPerChat: 1, BatchSize: 2}
	deleted, err := DeleteExpiredMessages(context.Background(), DB, now, policy)
	if err != nil {
		t.Fatalf("batched retention prune failed: %v", err)
	}
	if deleted != 8 {
		t.Fatalf("deleted %d message(s), want 8 (9 old minus a floor of 1)", deleted)
	}
	if remaining := countRetentionMessages(t, DB, chat); remaining != 1 {
		t.Fatalf("chat has %d rows left, want the 1-message hot floor", remaining)
	}
}

// TestDeleteExpiredMessagesDisabledIsNoOp keeps a disabled policy (or a missing
// db handle) from touching the database at all.
func TestDeleteExpiredMessagesDisabledIsNoOp(t *testing.T) {
	DB := setupMessageRetentionDB(t)
	now := time.Now().UTC()

	chat := createRetentionTestChat(t, DB, "disabled")
	seedRetentionMessages(t, DB, chat, 5, now.Add(-100*24*time.Hour))

	for _, policy := range []MessageRetentionPolicy{
		{},
		{Window: -time.Hour},
	} {
		deleted, err := DeleteExpiredMessages(context.Background(), DB, now, policy)
		if err != nil {
			t.Fatalf("disabled policy returned an error: %v", err)
		}
		if deleted != 0 {
			t.Fatalf("disabled policy deleted %d row(s), want 0", deleted)
		}
	}
	if remaining := countRetentionMessages(t, DB, chat); remaining != 5 {
		t.Fatalf("disabled policy changed the table: %d rows left, want 5", remaining)
	}
}

// TestDeleteExpiredMessagesHandlesNilDB keeps a nil handle a no-op instead of a
// panic, so the sweeper can be started unconditionally.
func TestDeleteExpiredMessagesHandlesNilDB(t *testing.T) {
	deleted, err := DeleteExpiredMessages(context.Background(), nil, time.Now(), MessageRetentionPolicy{Window: time.Hour})
	if err != nil || deleted != 0 {
		t.Fatalf("nil db = (%d, %v), want (0, nil)", deleted, err)
	}
}

// TestDeleteExpiredMessagesHonoursCancelledContext guards the shutdown path: a
// cancelled context must abort before the first write.
func TestDeleteExpiredMessagesHonoursCancelledContext(t *testing.T) {
	DB := setupMessageRetentionDB(t)
	now := time.Now().UTC()

	chat := createRetentionTestChat(t, DB, "cancelled")
	seedRetentionMessages(t, DB, chat, 5, now.Add(-100*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	policy := MessageRetentionPolicy{Window: time.Hour, HotFloorPerChat: 1, BatchSize: 10}
	if _, err := DeleteExpiredMessages(ctx, DB, now, policy); err == nil {
		t.Fatal("expected the cancelled context to abort the prune")
	}
	if remaining := countRetentionMessages(t, DB, chat); remaining != 5 {
		t.Fatalf("the aborted prune changed the table: %d rows left, want 5", remaining)
	}
}

// TestStartMessageRetentionSweeperPrunesAndStops checks the background loop end
// to end: it prunes on its own interval and its stop function is blocking and
// idempotent, so no prune outlives the server (the ci#811 teardown contract).
func TestStartMessageRetentionSweeperPrunesAndStops(t *testing.T) {
	DB := setupMessageRetentionDB(t)
	now := time.Now().UTC()

	chat := createRetentionTestChat(t, DB, "swept")
	seedRetentionMessages(t, DB, chat, 8, now.Add(-100*24*time.Hour))
	seedRetentionMessages(t, DB, chat, 1, now.Add(-time.Minute))

	policy := MessageRetentionPolicy{Window: 24 * time.Hour, HotFloorPerChat: 1, BatchSize: 50}
	stop := StartMessageRetentionSweeper(DB, 5*time.Millisecond, policy)

	deadline := time.Now().Add(10 * time.Second)
	for countRetentionMessages(t, DB, chat) != 1 {
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("the sweeper never pruned the old messages (still %d rows)", countRetentionMessages(t, DB, chat))
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	stop() // idempotent
}

// TestStartMessageRetentionSweeperDisabled keeps a disabled sweeper a no-op so
// the server can start it unconditionally, and a nil handle cannot panic.
func TestStartMessageRetentionSweeperDisabled(t *testing.T) {
	StartMessageRetentionSweeper(nil, time.Hour, MessageRetentionPolicy{Window: time.Hour})()
	StartMessageRetentionSweeper(nil, 0, MessageRetentionPolicy{Window: time.Hour})()

	DB := setupMessageRetentionDB(t)
	now := time.Now().UTC()
	chat := createRetentionTestChat(t, DB, "sweeper-disabled")
	seedRetentionMessages(t, DB, chat, 4, now.Add(-100*24*time.Hour))

	StartMessageRetentionSweeper(DB, time.Hour, MessageRetentionPolicy{Window: 0})()
	StartMessageRetentionSweeper(DB, 0, MessageRetentionPolicy{Window: time.Hour})()
	StartMessageRetentionSweeper(DB, -time.Second, MessageRetentionPolicy{Window: time.Hour})()

	if remaining := countRetentionMessages(t, DB, chat); remaining != 4 {
		t.Fatalf("a disabled sweeper pruned rows: %d left, want 4", remaining)
	}
}
