package chats

import (
	"backend/api/websocket"
	"backend/database"
	"backend/nativecoder"
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
	DB := database.SetupDatabase(cfg)
	closeChatsTestDB(t, DB)
	return DB
}

// closeChatsTestDB closes the connection pool behind a test database when the
// test finishes. t.TempDir registers its RemoveAll cleanup *before* this one is
// registered and cleanups run LIFO, so the pool is already closed by the time
// the temporary directory is removed.
//
// Without it the pool outlives the test: SQLite runs in WAL mode (see
// sqliteDSNPragmas), so a pooled connection can recreate the -wal/-shm sidecars
// while RemoveAll is walking the directory. That races into
// "TempDir RemoveAll cleanup: unlinkat ...: directory not empty" and, once the
// main database file is gone underneath a still-open connection, into
// "disk I/O error (522)" (observed flaking in CI on
// TestApproveInteractionConfirmationAppliesOverrides).
func closeChatsTestDB(t *testing.T, DB *gorm.DB) {
	t.Helper()
	if DB == nil {
		return
	}
	t.Cleanup(func() {
		sqlDB, err := DB.DB()
		if err != nil {
			return
		}
		_ = sqlDB.Close()
	})
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

func TestCreateChatKeepsChatBackendWithoutModelBindingOverride(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-opencode@example.com", false)
	botUser := createUserForChatsTest(t, DB, "bot-opencode@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	// A default LLM model config whose model_id matches the opencode bot's model.
	// Without the chat_backend skip this would rewrite backend to litellm and
	// inject its endpoint.
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
		"chat_backend":      "opencode",
		"backend":           "deepinfra",
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
	if chatBackend, _ := config["chat_backend"].(string); chatBackend != "opencode" {
		t.Fatalf("expected chat_backend to be preserved, got %v", config["chat_backend"])
	}
	if backend, _ := config["backend"].(string); backend != "deepinfra" {
		t.Fatalf("expected the LLM provider backend to be preserved, got %v", config["backend"])
	}
	if model, _ := config["model"].(string); model != "qwen3-4b-instruct-2507_vllm" {
		t.Fatalf("expected opencode model to pass through unchanged, got %v", config["model"])
	}
	if _, ok := config["endpoint"]; ok {
		t.Fatalf("expected no endpoint injected for chat_backend routing, got %v", config["endpoint"])
	}
}

// TestCreateChatSnapshotsToolCallBudget is the ci#709 regression on the chat
// side: a created chat carries a per-chat tool-call budget snapshot, so the
// budget it was started with is kept across a re-attach / re-provision and is
// reported as structured state (the resolved caps and their source) instead of
// only ever appearing inside an abort error string.
func TestCreateChatSnapshotsToolCallBudget(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-budget@example.com", false)
	botUser := createUserForChatsTest(t, DB, "bot-budget@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	// The bot's caps at creation time: 400 total / 25 failed.
	defaultConfig := map[string]interface{}{
		"model":                "qwen3-4b-instruct-2507_vllm",
		"backend":              "litellm",
		"tool_call_max_total":  400,
		"tool_call_max_failed": 25,
	}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)
	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                "bot-runtime-budget",
		Description:         "runtime",
		DefaultSharedConfig: defaultConfigJSON,
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

	snapshot, ok := nativecoder.ToolCallBudgetSnapshot(config)
	if !ok {
		t.Fatalf("expected a per-chat tool-call budget snapshot, got %#v", config[nativecoder.ToolCallBudgetConfigKey])
	}
	// The snapshot keeps the caps the chat was created with...
	if snapshot.MaxTotal != 400 || snapshot.MaxFailed != 25 {
		t.Fatalf("expected the bot's caps at creation time in the snapshot, got %+v", snapshot)
	}

	// ...while the *effective* caps are the fleet-wide floor of the resolved
	// sources (ci#709): a chat snapshotted with a smaller budget than the
	// compiled-in default is lifted to that default, and a larger declared cap
	// would win. Resolve both sides against the constants so raising the floor
	// (500/20 as of #931) keeps this regression honest.
	wantTotal, wantFailed := 400, 25
	if nativecoder.DefaultToolCallMaxTotal > wantTotal {
		wantTotal = nativecoder.DefaultToolCallMaxTotal
	}
	if nativecoder.DefaultToolCallMaxFailed > wantFailed {
		wantFailed = nativecoder.DefaultToolCallMaxFailed
	}
	resolved := nativecoder.ResolveToolCallBudget(config, nil)
	if resolved.MaxTotal != wantTotal || resolved.MaxFailed != wantFailed {
		t.Fatalf("expected the effective budget %d/%d, got %+v", wantTotal, wantFailed, resolved)
	}
	if resolved.SnapshotMaxTotal != 400 || resolved.SnapshotMaxFailed != 25 {
		t.Fatalf("expected the snapshot to stay reported even when the floor is higher, got %+v", resolved)
	}
	meta := resolved.Metadata()
	if meta["snapshot_max_total"] != 400 || meta["snapshot_max_failed"] != 25 {
		t.Fatalf("expected the snapshot in the structured metadata, got %#v", meta)
	}
}
