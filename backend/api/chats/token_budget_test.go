package chats

import (
	"testing"

	"backend/database"
)

func TestInteractionTokensUsedSumsMessageUsage(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "tb-owner@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "tb-bot@example.com")

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	messages := []database.Message{
		{ChatId: chat.ID, SenderId: bot.ID, MetaData: database.JSONRaw(`{"token_usage":{"prompt_tokens":900,"completion_tokens":100,"total_tokens":1000}}`)},
		{ChatId: chat.ID, SenderId: bot.ID, MetaData: database.JSONRaw(`{"token_usage":{"prompt_tokens":2000,"completion_tokens":500,"total_tokens":2500}}`)},
		{ChatId: chat.ID, SenderId: owner.ID, MetaData: database.JSONRaw(`{"finished":true}`)}, // no usage
	}
	for i := range messages {
		if err := DB.Create(&messages[i]).Error; err != nil {
			t.Fatalf("failed to create message: %v", err)
		}
	}

	if got := InteractionTokensUsed(DB, chat.ID); got != 3500 {
		t.Fatalf("InteractionTokensUsed = %d, want 3500", got)
	}
	if got := InteractionTokensUsed(DB, 0); got != 0 {
		t.Fatalf("InteractionTokensUsed(nil chat) = %d, want 0", got)
	}
	if got := InteractionTokensUsed(nil, chat.ID); got != 0 {
		t.Fatalf("InteractionTokensUsed(nil db) = %d, want 0", got)
	}
}

func TestResolveAutoContinueConfigTokenBudget(t *testing.T) {
	top := ResolveAutoContinueConfig(map[string]interface{}{"auto_continue": true, "token_budget": 4200})
	if top.TokenBudget != 4200 {
		t.Fatalf("top-level TokenBudget = %d, want 4200", top.TokenBudget)
	}

	dispatch := ResolveAutoContinueConfig(map[string]interface{}{
		"agent_dispatch": map[string]interface{}{"auto_continue": true, "token_budget": 9000},
	})
	if dispatch.TokenBudget != 9000 {
		t.Fatalf("agent_dispatch TokenBudget = %d, want 9000", dispatch.TokenBudget)
	}

	unset := ResolveAutoContinueConfig(map[string]interface{}{"auto_continue": true})
	if unset.TokenBudget != 0 {
		t.Fatalf("unset TokenBudget = %d, want 0 (unlimited)", unset.TokenBudget)
	}
}
