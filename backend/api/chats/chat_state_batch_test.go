package chats

import (
	"path/filepath"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

func openBatchStatesTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "chat_states_batch_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	DB := database.SetupDatabase(cfg)
	closeChatsTestDB(t, DB)
	return DB
}

// TestResolveChatStatesBatchMatchesPerChatResolution pins that the batched
// transcript-tail read used by the sidebar list and the /chats/states poll is
// behaviour-preserving: every chat must resolve to exactly the state the
// per-chat resolver produces.
func TestResolveChatStatesBatchMatchesPerChatResolution(t *testing.T) {
	DB := openBatchStatesTestDB(t)
	owner := createUserForChatsTest(t, DB, "batch-owner", false)
	bot := createUserForChatsTest(t, DB, "batch-bot", false)

	// A resting chat with a finished agent turn, and an interaction chat with a
	// plain prompt. Both must agree across the two resolution paths.
	chats := []database.Chat{
		{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"},
		{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction:opencode"},
	}
	finished := "This is done."
	bodies := []string{`{"finished":true}`, `{}`}
	for i := range chats {
		if err := DB.Create(&chats[i]).Error; err != nil {
			t.Fatalf("create chat: %v", err)
		}
		message := database.Message{
			SenderId:   bot.ID,
			ReceiverId: owner.ID,
			ChatId:     chats[i].ID,
			Text:       &finished,
			MetaData:   database.JSONRaw(bodies[i]),
		}
		if err := DB.Create(&message).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}

	batchTails := loadChatMessageTails(DB, chats)
	if batchTails == nil {
		t.Fatal("expected a batched transcript tail, got no batch")
	}
	for _, chat := range chats {
		if _, _, ok := batchTails.lookup(chat.ID); !ok {
			t.Fatalf("chat %d missing from the batch tails", chat.ID)
		}
	}

	batched := ResolveChatStates(DB, nil, chats)
	for _, chat := range chats {
		status, err := resolveInteractionStatus(DB, nil, chat)
		if err != nil {
			t.Fatalf("per-chat resolution of chat %d: %v", chat.ID, err)
		}
		if got, want := batched[chat.UUID], status.State; got != want {
			t.Fatalf("chat %d (%s): batched state %q != per-chat state %q", chat.ID, chat.ChatType, got, want)
		}
	}
}

// TestChatMessageTailsCoverEmptyChats pins that a chat without messages is
// present in the batch, so the resolver does not issue a per-chat query for it.
func TestChatMessageTailsCoverEmptyChats(t *testing.T) {
	DB := openBatchStatesTestDB(t)
	owner := createUserForChatsTest(t, DB, "tails-owner", false)
	bot := createUserForChatsTest(t, DB, "tails-bot", false)
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	tails := loadChatMessageTails(DB, []database.Chat{chat})
	messages, err, ok := tails.lookup(chat.ID)
	if !ok {
		t.Fatalf("empty chat %d missing from the batch", chat.ID)
	}
	if err != nil {
		t.Fatalf("lookup error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected an empty tail, got %d messages", len(messages))
	}
}
