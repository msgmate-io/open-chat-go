package msgmate

import (
	"backend/database"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupBotProfilesTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "bot_profiles_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createBotProfilesTestBot(t *testing.T, DB *gorm.DB, name string, defaultSharedConfig map[string]interface{}) database.User {
	t.Helper()

	botUser := database.User{
		Name:         name,
		Username:     name,
		Email:        name + "@bot.local",
		PasswordHash: "unused",
		ContactToken: name + "-contact-token",
		IsAutomated:  true,
	}
	if err := DB.Create(&botUser).Error; err != nil {
		t.Fatalf("failed creating bot user: %v", err)
	}

	owner := database.User{
		Name:         name + "-owner",
		Username:     name + "-owner",
		Email:        name + "-owner@example.com",
		PasswordHash: "unused",
		ContactToken: name + "-owner-contact-token",
	}
	if err := DB.Create(&owner).Error; err != nil {
		t.Fatalf("failed creating owner user: %v", err)
	}

	configJSON, err := json.Marshal(defaultSharedConfig)
	if err != nil {
		t.Fatalf("failed marshaling default shared config: %v", err)
	}
	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                name + "_runtime",
		Description:         "test runtime",
		DefaultSharedConfig: configJSON,
		IsPublic:            false,
		IsActive:            true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed creating bot runtime config: %v", err)
	}

	return botUser
}

func createBotProfilesTestModelConfig(t *testing.T, DB *gorm.DB, botUsername string, configuration string) {
	t.Helper()
	modelConfig := database.ModelConfig{
		Title:         "assigned-model",
		Description:   "assigned model config",
		ModelID:       "assigned-model-id",
		Configuration: json.RawMessage(configuration),
		BotUsernames:  database.StringSliceJSON{botUsername},
		IsPublic:      true,
		IsDefault:     true,
	}
	if err := DB.Create(&modelConfig).Error; err != nil {
		t.Fatalf("failed creating model config: %v", err)
	}
}

func readBotProfilesTestModels(t *testing.T, DB *gorm.DB, botUser database.User) []BotModel {
	t.Helper()
	var profile database.PublicProfile
	if err := DB.Where("user_id = ?", botUser.ID).First(&profile).Error; err != nil {
		t.Fatalf("expected bot public profile to exist: %v", err)
	}
	var profileData struct {
		Models []BotModel `json:"models"`
	}
	if err := json.Unmarshal(profile.ProfileData, &profileData); err != nil {
		t.Fatalf("failed decoding profile data: %v", err)
	}
	return profileData.Models
}

func TestCreateOrUpdateBotProfileKeepsRegisteredChatBackend(t *testing.T) {
	RegisterChatBackend("profiletestbackend", func(_ context.Context, _ ChatBackendRequest) error {
		return nil
	})

	DB := setupBotProfilesTestDB(t)
	botUser := createBotProfilesTestBot(t, DB, "profile-backend-bot", map[string]interface{}{
		"backend": "profiletestbackend",
		"model":   "runtime-default-model",
	})
	createBotProfilesTestModelConfig(t, DB, botUser.Name, `{"backend":"litellm","model":"assigned-model-id","endpoint":"https://example.com/v1"}`)

	if err := CreateOrUpdateBotProfile(DB, botUser); err != nil {
		t.Fatalf("CreateOrUpdateBotProfile failed: %v", err)
	}

	models := readBotProfilesTestModels(t, DB, botUser)
	if len(models) != 1 {
		t.Fatalf("expected 1 profile model, got %d", len(models))
	}
	if got := models[0].Configuration.Backend; got != "profiletestbackend" {
		t.Fatalf("expected profile model backend to stay %q, got %q", "profiletestbackend", got)
	}
	if got := models[0].Configuration.Model; got != "assigned-model-id" {
		t.Fatalf("expected profile model to keep the assigned model id, got %q", got)
	}
}

func TestCreateOrUpdateBotProfileKeepsRegisteredChatBackendOnFallback(t *testing.T) {
	RegisterChatBackend("profiletestfallbackbackend", func(_ context.Context, _ ChatBackendRequest) error {
		return nil
	})

	DB := setupBotProfilesTestDB(t)
	botUser := createBotProfilesTestBot(t, DB, "profile-fallback-bot", map[string]interface{}{
		"backend": "profiletestfallbackbackend",
		"model":   "runtime-default-model",
	})

	if err := CreateOrUpdateBotProfile(DB, botUser); err != nil {
		t.Fatalf("CreateOrUpdateBotProfile failed: %v", err)
	}

	models := readBotProfilesTestModels(t, DB, botUser)
	if len(models) != 1 {
		t.Fatalf("expected 1 fallback profile model, got %d", len(models))
	}
	if got := models[0].Configuration.Backend; got != "profiletestfallbackbackend" {
		t.Fatalf("expected fallback profile model backend %q, got %q", "profiletestfallbackbackend", got)
	}
}

func TestCreateOrUpdateBotProfileLeavesLLMBotBackendsUntouched(t *testing.T) {
	DB := setupBotProfilesTestDB(t)
	botUser := createBotProfilesTestBot(t, DB, "llm-backend-bot", map[string]interface{}{
		"backend": "litellm",
		"model":   "runtime-default-model",
	})
	createBotProfilesTestModelConfig(t, DB, botUser.Name, `{"backend":"deepinfra","model":"assigned-model-id","endpoint":"https://api.deepinfra.com/v1/openai"}`)

	if err := CreateOrUpdateBotProfile(DB, botUser); err != nil {
		t.Fatalf("CreateOrUpdateBotProfile failed: %v", err)
	}

	models := readBotProfilesTestModels(t, DB, botUser)
	if len(models) != 1 {
		t.Fatalf("expected 1 profile model, got %d", len(models))
	}
	if got := models[0].Configuration.Backend; got != "deepinfra" {
		t.Fatalf("expected LLM bot profile model backend to stay %q, got %q", "deepinfra", got)
	}
}
