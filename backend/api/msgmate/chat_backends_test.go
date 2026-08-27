package msgmate

import (
	"backend/database"
	"context"
	"encoding/json"
	"testing"

	"gorm.io/gorm"
)

func createChatBackendTestChat(t *testing.T, DB *gorm.DB, config map[string]interface{}) database.Chat {
	t.Helper()

	user1 := database.User{Name: "chat-backend-user1", Username: "chat-backend-user1", Email: "chat-backend-user1@example.com", PasswordHash: "unused", ContactToken: "chat-backend-user1-token"}
	user2 := database.User{Name: "chat-backend-user2", Username: "chat-backend-user2", Email: "chat-backend-user2@example.com", PasswordHash: "unused", ContactToken: "chat-backend-user2-token"}
	if err := DB.Create(&user1).Error; err != nil {
		t.Fatalf("failed creating user1: %v", err)
	}
	if err := DB.Create(&user2).Error; err != nil {
		t.Fatalf("failed creating user2: %v", err)
	}

	chat := database.Chat{User1Id: user1.ID, User2Id: user2.ID, ChatType: "interaction"}
	if config != nil {
		configData, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed marshaling shared config: %v", err)
		}
		sharedConfig := database.SharedChatConfig{ConfigData: configData}
		if err := DB.Create(&sharedConfig).Error; err != nil {
			t.Fatalf("failed creating shared config: %v", err)
		}
		chat.SharedConfigId = &sharedConfig.ID
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed creating chat: %v", err)
	}
	return chat
}

func TestResolveChatBackendUsesChatBackendKey(t *testing.T) {
	called := false
	RegisterChatBackend("resolvechatbackendtest", func(_ context.Context, _ ChatBackendRequest) error {
		called = true
		return nil
	})

	DB := setupBotProfilesTestDB(t)
	chat := createChatBackendTestChat(t, DB, map[string]interface{}{
		"chat_backend": "resolvechatbackendtest",
		"backend":      "deepinfra",
		"model":        "Qwen/Qwen3.8-27B",
	})

	fn, config, ok := ResolveChatBackend(DB, chat.UUID)
	if !ok || fn == nil {
		t.Fatalf("expected chat backend to resolve via chat_backend key")
	}
	if err := fn(context.Background(), ChatBackendRequest{}); err != nil {
		t.Fatalf("chat backend call failed: %v", err)
	}
	if !called {
		t.Fatalf("expected resolved chat backend to be invoked")
	}
	if got, _ := config["backend"].(string); got != "deepinfra" {
		t.Fatalf("expected config to keep the LLM provider backend %q, got %q", "deepinfra", got)
	}
}

func TestResolveChatBackendFallsBackToLegacyBackendKey(t *testing.T) {
	RegisterChatBackend("resolvelegacybackendtest", func(_ context.Context, _ ChatBackendRequest) error {
		return nil
	})

	DB := setupBotProfilesTestDB(t)
	chat := createChatBackendTestChat(t, DB, map[string]interface{}{
		"backend": "resolvelegacybackendtest",
		"model":   "legacy-model",
	})

	fn, _, ok := ResolveChatBackend(DB, chat.UUID)
	if !ok || fn == nil {
		t.Fatalf("expected chat backend to resolve via legacy backend key")
	}
}

func TestResolveChatBackendIgnoresLLMProviderBackends(t *testing.T) {
	DB := setupBotProfilesTestDB(t)
	chat := createChatBackendTestChat(t, DB, map[string]interface{}{
		"backend": "deepinfra",
		"model":   "Qwen/Qwen3.8-27B",
	})

	if fn, _, ok := ResolveChatBackend(DB, chat.UUID); ok || fn != nil {
		t.Fatalf("expected no chat backend for a plain LLM provider config")
	}
}

func TestResolveChatBackendWithoutSharedConfig(t *testing.T) {
	DB := setupBotProfilesTestDB(t)
	chat := createChatBackendTestChat(t, DB, nil)

	if fn, _, ok := ResolveChatBackend(DB, chat.UUID); ok || fn != nil {
		t.Fatalf("expected no chat backend for a chat without shared config")
	}
}
