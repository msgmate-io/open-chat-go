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
		"model":         "qwen3-4b-instruct-2507_vllm",
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
	if model, _ := config["model"].(string); model != "qwen3-4b-instruct-2507_vllm" {
		t.Fatalf("expected fallback model in chat config, got %v", config["model"])
	}
}

func TestCreateChatMergesProvidedSharedConfigWithAutomatedBotDefaults(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-merge@example.com", false)
	botUser := createUserForChatsTest(t, DB, "bot-merge@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	defaultConfig := map[string]interface{}{
		"model":   "qwen3-4b-instruct-2507_vllm",
		"backend": "litellm",
		"tools":   []string{"mcp:figma5:get_metadata"},
		"integrations": []string{
			"figma5",
		},
		"mcp_tools": map[string]interface{}{
			"mcp:figma5:get_metadata": map[string]interface{}{
				"name":             "mcp:figma5:get_metadata",
				"integration_name": "figma5",
				"remote_tool_name": "get_metadata",
			},
		},
	}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)

	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                "bot-runtime-merge",
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
		"shared_config": map[string]interface{}{
			"model":   "gpt-4o-mini",
			"backend": "openai",
		},
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
	if model, _ := config["model"].(string); model != "gpt-4o-mini" {
		t.Fatalf("expected provided model override, got %v", config["model"])
	}
	if backend, _ := config["backend"].(string); backend != "openai" {
		t.Fatalf("expected provided backend override, got %v", config["backend"])
	}
	if _, ok := config["mcp_tools"].(map[string]interface{}); !ok {
		t.Fatalf("expected merged mcp_tools in chat config")
	}
	if _, ok := config["integrations"].([]interface{}); !ok {
		t.Fatalf("expected merged integrations array in chat config")
	}
}

func TestCreateChatAppliesModelConfigBackendBindingForAutomatedBot(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-modelbind@example.com", false)
	botUser := createUserForChatsTest(t, DB, "bot-modelbind@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	modelCfg := map[string]interface{}{
		"model":    "deepseek-ai/DeepSeek-V4-Flash",
		"backend":  "deepinfra",
		"endpoint": "https://api.deepinfra.com/v1/openai",
	}
	modelCfgJSON, _ := json.Marshal(modelCfg)
	if err := DB.Create(&database.ModelConfig{
		Title:         "DeepSeek Flash",
		Description:   "test",
		ModelID:       "deepseek-ai/DeepSeek-V4-Flash",
		Configuration: modelCfgJSON,
		IsPublic:      true,
		IsDefault:     false,
	}).Error; err != nil {
		t.Fatalf("failed creating model config: %v", err)
	}

	defaultConfig := map[string]interface{}{
		"model":    "deepseek-ai/DeepSeek-V4-Flash",
		"backend":  "litellm",
		"endpoint": "https://litellm.t1m.me/v1",
	}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)

	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                "bot-runtime-modelbind",
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
		"shared_config": map[string]interface{}{
			"model": "deepseek-ai/DeepSeek-V4-Flash",
		},
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
	if backend, _ := config["backend"].(string); backend != "deepinfra" {
		t.Fatalf("expected backend from model config binding, got %v", config["backend"])
	}
	if endpoint, _ := config["endpoint"].(string); endpoint != "https://api.deepinfra.com/v1/openai" {
		t.Fatalf("expected endpoint from model config binding, got %v", config["endpoint"])
	}
}

func TestCreateChatKeepsTestbackendWithoutModelBindingOverride(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-testbackend@example.com", false)
	botUser := createUserForChatsTest(t, DB, "bot-testbackend@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	modelCfg := map[string]interface{}{
		"model":    "qwen3-4b-instruct-2507_vllm",
		"backend":  "litellm",
		"endpoint": "https://litellm.t1m.me/v1",
	}
	modelCfgJSON, _ := json.Marshal(modelCfg)
	if err := DB.Create(&database.ModelConfig{
		Title:         "Qwen",
		Description:   "test",
		ModelID:       "qwen3-4b-instruct-2507_vllm",
		Configuration: modelCfgJSON,
		IsPublic:      true,
		IsDefault:     false,
	}).Error; err != nil {
		t.Fatalf("failed creating model config: %v", err)
	}

	defaultConfig := map[string]interface{}{
		"model":    "qwen3-4b-instruct-2507_vllm",
		"backend":  "testbackend",
		"endpoint": "http://testbackend.local/v1",
	}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)

	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                "bot-runtime-testbackend",
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
	if backend, _ := config["backend"].(string); backend != "testbackend" {
		t.Fatalf("expected testbackend to be preserved, got %v", config["backend"])
	}
	if endpoint, _ := config["endpoint"].(string); endpoint != "http://testbackend.local/v1" {
		t.Fatalf("expected testbackend endpoint to be preserved, got %v", config["endpoint"])
	}
}

func TestCreateChatKeepsOpencodeBackendWithoutModelBindingOverride(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-opencode@example.com", false)
	botUser := createUserForChatsTest(t, DB, "bot-opencode@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	// A default LLM model config whose model_id matches the opencode bot's model.
	// Without the opencode skip this would rewrite backend to litellm.
	modelCfg := map[string]interface{}{
		"model":    "qwen3-4b-instruct-2507_vllm",
		"backend":  "litellm",
		"endpoint": "https://litellm.t1m.me/v1",
	}
	modelCfgJSON, _ := json.Marshal(modelCfg)
	if err := DB.Create(&database.ModelConfig{
		Title:         "Qwen",
		Description:   "test",
		ModelID:       "qwen3-4b-instruct-2507_vllm",
		Configuration: modelCfgJSON,
		IsPublic:      true,
		IsDefault:     false,
	}).Error; err != nil {
		t.Fatalf("failed creating model config: %v", err)
	}

	defaultConfig := map[string]interface{}{
		"model":             "qwen3-4b-instruct-2507_vllm",
		"backend":           "opencode",
		"persist_tool_init": true,
		"tools":             []string{"opencode_select_project"},
	}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)

	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                "bot-runtime-opencode",
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
	if backend, _ := config["backend"].(string); backend != "opencode" {
		t.Fatalf("expected opencode backend to be preserved, got %v", config["backend"])
	}
	if model, _ := config["model"].(string); model != "qwen3-4b-instruct-2507_vllm" {
		t.Fatalf("expected opencode model to pass through unchanged, got %v", config["model"])
	}
	if _, ok := config["endpoint"]; ok {
		t.Fatalf("expected no endpoint injected for opencode backend, got %v", config["endpoint"])
	}
}
