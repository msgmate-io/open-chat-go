package database

import (
	"backend/runtimecfg"
	"fmt"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setRuntimeValuesForTest(t *testing.T, overrides map[string]string) {
	t.Helper()
	prev := runtimecfg.GetAll()
	next := make(map[string]runtimecfg.Value, len(prev)+len(overrides))
	for key, value := range prev {
		next[key] = value
	}
	for key, value := range overrides {
		next[key] = runtimecfg.Value{Value: value, Sensitive: true}
	}
	runtimecfg.SetAll(next)
	t.Cleanup(func() {
		runtimecfg.SetAll(prev)
	})
}

func createDefaultModelForTest(t *testing.T, DB *gorm.DB, modelID string, backend string, botUsernames []string, rawCfg string) {
	t.Helper()
	configuration := rawCfg
	if configuration == "" {
		configuration = fmt.Sprintf(`{"model":"%s","backend":"%s","endpoint":"https://example.invalid/v1"}`, modelID, backend)
	}
	record := ModelConfig{
		Title:         modelID,
		Description:   "test",
		ModelID:       modelID,
		Configuration: []byte(configuration),
		BotUsernames:  StringSliceJSON(botUsernames),
		IsPublic:      true,
		IsDefault:     true,
	}
	if err := DB.Create(&record).Error; err != nil {
		t.Fatalf("failed creating model config %q: %v", modelID, err)
	}
}

func TestSyncDefaultBotModelsByProviderKeys(t *testing.T) {
	setRuntimeValuesForTest(t, map[string]string{
		"OPENAI_API_KEY":    "test-openai-key",
		"ANTHROPIC_API_KEY": "",
		"DEEPINFRA_API_KEY": "",
		"GROQ_API_KEY":      "",
		"LITELLM_API_KEY":   "",
	})

	DB := SetupDatabase(DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "provider_key_sync.db"),
		Debug:    false,
		ResetDB:  true,
	})

	createDefaultModelForTest(t, DB, "openai-unassigned", "openai", []string{}, "")
	createDefaultModelForTest(t, DB, "openai-assigned", "openai", []string{"bot"}, "")
	createDefaultModelForTest(t, DB, "custom-assigned", "custom-provider", []string{"bot"}, "")
	createDefaultModelForTest(t, DB, "invalid-config", "", []string{"bot"}, `{"backend":`)

	first, err := SyncDefaultBotModelsByProviderKeys(DB, "bot")
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if first.Assigned != 1 || first.Unassigned != 0 {
		t.Fatalf("unexpected first sync counts: %+v", first)
	}

	var openaiUnassigned ModelConfig
	if err := DB.Where("model_id = ?", "openai-unassigned").First(&openaiUnassigned).Error; err != nil {
		t.Fatalf("failed loading openai-unassigned: %v", err)
	}
	if !openaiUnassigned.AssignedToBot("bot") {
		t.Fatalf("expected openai-unassigned to be assigned when OPENAI_API_KEY is set")
	}

	setRuntimeValuesForTest(t, map[string]string{"OPENAI_API_KEY": ""})

	second, err := SyncDefaultBotModelsByProviderKeys(DB, "bot")
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	if second.Assigned != 0 || second.Unassigned != 2 {
		t.Fatalf("unexpected second sync counts: %+v", second)
	}

	var openaiAssigned ModelConfig
	if err := DB.Where("model_id = ?", "openai-assigned").First(&openaiAssigned).Error; err != nil {
		t.Fatalf("failed loading openai-assigned: %v", err)
	}
	if openaiAssigned.AssignedToBot("bot") {
		t.Fatalf("expected openai-assigned to be unassigned when OPENAI_API_KEY is missing")
	}

	var customAssigned ModelConfig
	if err := DB.Where("model_id = ?", "custom-assigned").First(&customAssigned).Error; err != nil {
		t.Fatalf("failed loading custom-assigned: %v", err)
	}
	if !customAssigned.AssignedToBot("bot") {
		t.Fatalf("expected unmanaged provider assignment to remain unchanged")
	}

	var invalidConfig ModelConfig
	if err := DB.Where("model_id = ?", "invalid-config").First(&invalidConfig).Error; err != nil {
		t.Fatalf("failed loading invalid-config: %v", err)
	}
	if !invalidConfig.AssignedToBot("bot") {
		t.Fatalf("expected invalid config assignment to remain unchanged")
	}
}

func TestSyncDefaultBotModelsByProviderKeysRequiresBotUsername(t *testing.T) {
	DB := SetupDatabase(DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "provider_key_sync_empty_bot.db"),
		Debug:    false,
		ResetDB:  true,
	})

	if _, err := SyncDefaultBotModelsByProviderKeys(DB, "   "); err == nil {
		t.Fatal("expected error when bot username is empty")
	}
}
