package chats

import (
	"backend/api/websocket"
	"backend/database"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// ci#756: a chat created directly against a repository must receive the same
// pre-LLM dev setup (submodules + build tooling) as a dispatched interaction.
func TestCreateChatAppliesNativeCoderDevSetupFromRepoTags(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "devsetup-owner@example.com", false)
	botUser := createUserForChatsTest(t, DB, "devsetup-bot@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	defaultConfig := map[string]interface{}{
		"model":   "qwen3-4b-instruct-2507_vllm",
		"backend": "litellm",
		"tool_init": map[string]interface{}{
			"coder_select_workspace": map[string]interface{}{
				"git_repo": "https://github.com/msgmate-io/open-chat-go.git",
			},
		},
	}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)
	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                "devsetup-runtime",
		DefaultSharedConfig: database.JSONRaw(defaultConfigJSON),
		IsActive:            true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create bot runtime config: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"contact_token": botUser.ContactToken,
		"chat_type":     "conversation",
		"tags":          []string{"repo:msgmate-io/open-chat-go"},
	})
	req := httptest.NewRequest("POST", "/api/v1/chats/create", strings.NewReader(string(body)))
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
		t.Fatalf("failed to decode create response: %v", err)
	}
	config, ok := response["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected config object in response, got %s", rr.Body.String())
	}
	commands, _ := config["coder_sandbox_setup_commands"].(string)
	if commands == "" {
		t.Fatal("chat-create did not add the native-coder dev setup commands")
	}
	if !strings.Contains(commands, "openchat-integrations prepare --profile full-development") {
		t.Fatalf("dev setup does not target the open-chat-go full-development profile: %s", commands)
	}
}
