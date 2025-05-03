package chats

import (
	"backend/api/websocket"
	"backend/database"
	"backend/server/util"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupChatsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "chats_create_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createUserForChatsTest(t *testing.T, DB *gorm.DB, name string, isAdmin bool) *database.User {
	t.Helper()
	err, user := util.CreateUser(DB, name, "Passw0rd!", isAdmin)
	if err != nil {
		t.Fatalf("failed to create user %q: %v", name, err)
	}
	return user
}

func TestCreateChatFallsBackToBotDefaultSharedConfig(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	botUser := createUserForChatsTest(t, DB, "bot@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	defaultConfig := map[string]interface{}{
		"model":         "qwen3-8b-instruct_vllm",
		"backend":       "litellm",
		"endpoint":      "https://litellm.t1m.me/v1",
		"temperature":   0.7,
		"max_tokens":    4096,
		"context":       10,
		"system_prompt": "You are a helpful assistant.",
	}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)

	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                "bot-runtime",
		Description:         "runtime",
		DefaultSharedConfig: defaultConfigJSON,
		IsPublic:            false,
		IsActive:            true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create bot runtime config: %v", err)
	}

	bodyPayload := map[string]interface{}{
		"contact_token": botUser.ContactToken,
		"chat_type":     "conversation",
	}
	body, _ := json.Marshal(bodyPayload)
	req := httptest.NewRequest("POST", "/api/v1/chats/create", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", owner)
	ctx = context.WithValue(ctx, "websocket", websocket.NewWebSocketHandler())
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.Create(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode create chat response: %v", err)
	}

	config, ok := response["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected config object in response")
	}
	if model, _ := config["model"].(string); model != "qwen3-8b-instruct_vllm" {
		t.Fatalf("expected fallback model in chat config, got %v", config["model"])
	}
}
