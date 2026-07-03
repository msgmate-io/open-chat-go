package bots

import (
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

func setupBotsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "bots_create_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createUserForBotsTest(t *testing.T, DB *gorm.DB, name string, isAdmin bool) *database.User {
	t.Helper()
	err, user := util.CreateUser(DB, name, "Passw0rd!", isAdmin)
	if err != nil {
		t.Fatalf("failed to create user %q: %v", name, err)
	}
	return user
}

func createBotRequestPayload(name string) map[string]interface{} {
	return map[string]interface{}{
		"name":        name,
		"description": "test bot",
		"default_shared_config": map[string]interface{}{
			"model":         "qwen3-8b-instruct_vllm",
			"backend":       "litellm",
			"endpoint":      "https://litellm.t1m.me/v1",
			"temperature":   0.7,
			"max_tokens":    4096,
			"context":       10,
			"system_prompt": "You are a helpful assistant.",
		},
	}
}

func TestCreateBotHidesGeneratedPasswordForNonAdmin(t *testing.T) {
	DB := setupBotsTestDB(t)
	user := createUserForBotsTest(t, DB, "owner@example.com", false)

	body, _ := json.Marshal(createBotRequestPayload("owner-bot"))
	req := httptest.NewRequest("POST", "/api/v1/bots", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.Create(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, exists := response["generated_password"]; exists {
		t.Fatalf("expected generated_password to be omitted for non-admin users")
	}

	botRaw, ok := response["bot"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected bot object in response")
	}
	contactToken, _ := botRaw["bot_contact_token"].(string)
	if contactToken == "" {
		t.Fatalf("expected bot_contact_token in response")
	}

	var botUser database.User
	if err := DB.Where("contact_token = ?", contactToken).First(&botUser).Error; err != nil {
		t.Fatalf("failed to load created bot user: %v", err)
	}

	var profile database.PublicProfile
	if err := DB.Where("user_id = ?", botUser.ID).First(&profile).Error; err != nil {
		t.Fatalf("expected bot public profile to be created: %v", err)
	}

	var profileData map[string]interface{}
	if err := json.Unmarshal(profile.ProfileData, &profileData); err != nil {
		t.Fatalf("failed to decode profile data: %v", err)
	}
	models, ok := profileData["models"].([]interface{})
	if !ok || len(models) == 0 {
		t.Fatalf("expected bot profile models to include at least one runtime config")
	}
}

func TestCreateBotShowsGeneratedPasswordForAdmin(t *testing.T) {
	DB := setupBotsTestDB(t)
	admin := createUserForBotsTest(t, DB, "admin@example.com", true)

	body, _ := json.Marshal(createBotRequestPayload("admin-bot"))
	req := httptest.NewRequest("POST", "/api/v1/bots", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", admin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.Create(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	password, ok := response["generated_password"].(string)
	if !ok || password == "" {
		t.Fatalf("expected generated_password for admin users")
	}
}
