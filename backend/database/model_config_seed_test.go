package database

import (
	"backend/runtimecfg"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func setExtraModelsJSONForTest(t *testing.T, value string) {
	t.Helper()
	prev := runtimecfg.GetAll()
	next := make(map[string]runtimecfg.Value, len(prev)+1)
	for key, cfg := range prev {
		next[key] = cfg
	}
	next["EXTRA_MODELS_JSON"] = runtimecfg.Value{Value: value, Sensitive: false}
	runtimecfg.SetAll(next)
	t.Cleanup(func() {
		runtimecfg.SetAll(prev)
	})
}

func parseExpectedModelEntries(t *testing.T, raw []byte, source string) []modelConfigFileEntry {
	t.Helper()
	entries, err := parseModelConfigFileEntries(raw, source)
	if err != nil {
		t.Fatalf("failed parsing %s: %v", source, err)
	}
	// SeedModelConfigs matches on model_id and keeps the first inserted row.
	deduped := make([]modelConfigFileEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		modelID := modelIDFromEntry(t, entry)
		if _, exists := seen[modelID]; exists {
			continue
		}
		seen[modelID] = struct{}{}
		deduped = append(deduped, entry)
	}
	return deduped
}

func writeExtraModelsFile(t *testing.T, entries []map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "extra_default_models_config.json")
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("failed marshaling extra models: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("failed writing extra models file: %v", err)
	}
	return path
}

func modelIDFromEntry(t *testing.T, entry modelConfigFileEntry) string {
	t.Helper()
	var cfg modelConfigID
	if err := json.Unmarshal(entry.Configuration, &cfg); err != nil {
		t.Fatalf("invalid configuration for %q: %v", entry.Title, err)
	}
	return cfg.Model
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func configurationMapsEqual(left, right json.RawMessage) bool {
	var leftMap map[string]interface{}
	var rightMap map[string]interface{}
	if err := json.Unmarshal(left, &leftMap); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightMap); err != nil {
		return false
	}
	return reflect.DeepEqual(leftMap, rightMap)
}

func TestSeedModelConfigsMatchesDefaultJSONAndBotAccess(t *testing.T) {
	setExtraModelsJSONForTest(t, "")

	dbPath := filepath.Join(t.TempDir(), "defaults.db")
	DB := SetupDatabase(DBConfig{
		Backend:  "sqlite",
		FilePath: dbPath,
		Debug:    false,
		ResetDB:  true,
	})

	if err := SeedModelConfigs(DB); err != nil {
		t.Fatalf("SeedModelConfigs failed: %v", err)
	}

	expected := parseExpectedModelEntries(t, defaultModelConfigsJSON, "default_model_configs.json")
	if len(expected) == 0 {
		t.Fatal("expected default_model_configs.json to contain models")
	}

	var rows []ModelConfig
	if err := DB.Where("owner_user_id IS NULL AND is_default = ?", true).Find(&rows).Error; err != nil {
		t.Fatalf("failed loading seeded models: %v", err)
	}
	if len(rows) != len(expected) {
		t.Fatalf("expected %d seeded default models, got %d", len(expected), len(rows))
	}

	byModelID := make(map[string]ModelConfig, len(rows))
	for _, row := range rows {
		byModelID[row.ModelID] = row
	}

	expectedBotModelIDs := map[string]struct{}{}
	for _, entry := range expected {
		modelID := modelIDFromEntry(t, entry)
		row, ok := byModelID[modelID]
		if !ok {
			t.Fatalf("missing seeded model %q", modelID)
		}
		if row.Title != entry.Title {
			t.Fatalf("model %q title: got %q want %q", modelID, row.Title, entry.Title)
		}
		if row.Description != entry.Description {
			t.Fatalf("model %q description mismatch", modelID)
		}
		if !row.IsDefault {
			t.Fatalf("model %q should be is_default=true", modelID)
		}
		wantPublic := true
		if entry.IsPublic != nil {
			wantPublic = *entry.IsPublic
		}
		if row.IsPublic != wantPublic {
			t.Fatalf("model %q is_public: got %v want %v", modelID, row.IsPublic, wantPublic)
		}
		if !reflect.DeepEqual(sortedCopy(row.BotUsernames), sortedCopy(entry.BotUsernames)) {
			t.Fatalf("model %q bot_usernames: got %v want %v", modelID, []string(row.BotUsernames), entry.BotUsernames)
		}
		if !configurationMapsEqual(row.Configuration, entry.Configuration) {
			t.Fatalf("model %q configuration mismatch", modelID)
		}
		for _, botUsername := range entry.BotUsernames {
			if botUsername == "bot" {
				expectedBotModelIDs[modelID] = struct{}{}
			}
		}
	}

	botUser := User{
		Name:         "bot",
		Username:     "bot",
		Email:        "bot@example.com",
		PasswordHash: "unused",
		ContactToken: "bot-contact-token",
		IsAutomated:  true,
	}
	if err := DB.Create(&botUser).Error; err != nil {
		t.Fatalf("failed creating default bot user: %v", err)
	}

	assigned, err := GetModelConfigsForBot(DB, "bot")
	if err != nil {
		t.Fatalf("GetModelConfigsForBot failed: %v", err)
	}
	if len(assigned) != len(expectedBotModelIDs) {
		t.Fatalf("expected bot access to %d models, got %d", len(expectedBotModelIDs), len(assigned))
	}
	for _, cfg := range assigned {
		if _, ok := expectedBotModelIDs[cfg.ModelID]; !ok {
			t.Fatalf("bot unexpectedly has access to model %q", cfg.ModelID)
		}
		if !cfg.AssignedToBot("bot") {
			t.Fatalf("model %q missing bot username assignment", cfg.ModelID)
		}
	}
}

func TestSeedModelConfigsLoadsExtraModelsOnRestartWithExistingDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "restart_models.db")

	// First boot uses the repo extra_default_models_config.json (currently empty).
	repoExtraPath := filepath.Join("data", "extra_default_models_config.json")
	setExtraModelsJSONForTest(t, repoExtraPath)

	DB := SetupDatabase(DBConfig{
		Backend:  "sqlite",
		FilePath: dbPath,
		Debug:    false,
		ResetDB:  true,
	})
	if err := SeedModelConfigs(DB); err != nil {
		t.Fatalf("initial SeedModelConfigs failed: %v", err)
	}

	var initialCount int64
	if err := DB.Model(&ModelConfig{}).Where("owner_user_id IS NULL AND is_default = ?", true).Count(&initialCount).Error; err != nil {
		t.Fatalf("failed counting initial models: %v", err)
	}
	expectedDefaults := parseExpectedModelEntries(t, defaultModelConfigsJSON, "default_model_configs.json")
	if int(initialCount) != len(expectedDefaults) {
		t.Fatalf("expected %d models after first boot, got %d", len(expectedDefaults), initialCount)
	}

	botUser := User{
		Name:         "bot",
		Username:     "bot",
		Email:        "bot@example.com",
		PasswordHash: "unused",
		ContactToken: "bot-contact-token-2",
		IsAutomated:  true,
	}
	if err := DB.Create(&botUser).Error; err != nil {
		t.Fatalf("failed creating default bot user: %v", err)
	}
	beforeExtraAccess, err := GetModelConfigsForBot(DB, "bot")
	if err != nil {
		t.Fatalf("GetModelConfigsForBot before extras failed: %v", err)
	}
	beforeExtraIDs := map[string]struct{}{}
	for _, cfg := range beforeExtraAccess {
		beforeExtraIDs[cfg.ModelID] = struct{}{}
	}

	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("failed getting sql DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("failed closing DB before restart: %v", err)
	}

	extraModelID := "integration-test-extra-model"
	extraPath := writeExtraModelsFile(t, []map[string]interface{}{
		{
			"title":         "Integration Test Extra Model",
			"description":   "Loaded from EXTRA_MODELS_JSON on restart",
			"bot_usernames": []string{"bot"},
			"is_public":     true,
			"configuration": map[string]interface{}{
				"model":         extraModelID,
				"backend":       "openai",
				"endpoint":      "https://api.openai.com/v1",
				"temperature":   0.2,
				"system_prompt": "You are a helpful assistant.",
			},
		},
	})
	setExtraModelsJSONForTest(t, extraPath)

	// Second boot against the already-initialized DB file.
	DB2 := SetupDatabase(DBConfig{
		Backend:  "sqlite",
		FilePath: dbPath,
		Debug:    false,
		ResetDB:  false,
	})
	if err := SeedModelConfigs(DB2); err != nil {
		t.Fatalf("restart SeedModelConfigs failed: %v", err)
	}

	var afterCount int64
	if err := DB2.Model(&ModelConfig{}).Where("owner_user_id IS NULL AND is_default = ?", true).Count(&afterCount).Error; err != nil {
		t.Fatalf("failed counting models after restart: %v", err)
	}
	if int(afterCount) != len(expectedDefaults)+1 {
		t.Fatalf("expected %d models after restart, got %d", len(expectedDefaults)+1, afterCount)
	}

	var extraRow ModelConfig
	if err := DB2.Where("owner_user_id IS NULL AND model_id = ?", extraModelID).First(&extraRow).Error; err != nil {
		t.Fatalf("expected extra model %q to be seeded on restart: %v", extraModelID, err)
	}
	if !extraRow.IsDefault || !extraRow.IsPublic {
		t.Fatalf("extra model flags: is_default=%v is_public=%v", extraRow.IsDefault, extraRow.IsPublic)
	}
	if !reflect.DeepEqual(sortedCopy(extraRow.BotUsernames), []string{"bot"}) {
		t.Fatalf("extra model bot_usernames: got %v want [bot]", []string(extraRow.BotUsernames))
	}

	afterExtraAccess, err := GetModelConfigsForBot(DB2, "bot")
	if err != nil {
		t.Fatalf("GetModelConfigsForBot after extras failed: %v", err)
	}
	if len(afterExtraAccess) != len(beforeExtraIDs)+1 {
		t.Fatalf("expected bot access count %d after extras, got %d", len(beforeExtraIDs)+1, len(afterExtraAccess))
	}
	foundExtraAccess := false
	for _, cfg := range afterExtraAccess {
		if cfg.ModelID == extraModelID {
			foundExtraAccess = true
			continue
		}
		if _, ok := beforeExtraIDs[cfg.ModelID]; !ok {
			t.Fatalf("unexpected new bot model access for %q", cfg.ModelID)
		}
	}
	if !foundExtraAccess {
		t.Fatalf("expected default bot to gain access to extra model %q", extraModelID)
	}
}

func TestSeedModelConfigsAcceptsInlineExtraModelsJSON(t *testing.T) {
	setExtraModelsJSONForTest(t, `[
	  {
	    "title": "Inline Extra",
	    "description": "from inline EXTRA_MODELS_JSON",
	    "bot_usernames": ["bot"],
	    "is_public": true,
	    "configuration": {
	      "model": "inline-extra-model",
	      "backend": "openai",
	      "endpoint": "https://api.openai.com/v1"
	    }
	  }
	]`)

	DB := SetupDatabase(DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "inline_extra.db"),
		Debug:    false,
		ResetDB:  true,
	})
	if err := SeedModelConfigs(DB); err != nil {
		t.Fatalf("SeedModelConfigs failed: %v", err)
	}

	var row ModelConfig
	if err := DB.Where("owner_user_id IS NULL AND model_id = ?", "inline-extra-model").First(&row).Error; err != nil {
		t.Fatalf("expected inline extra model to be seeded: %v", err)
	}
	if !row.AssignedToBot("bot") {
		t.Fatalf("expected inline extra model assigned to bot")
	}
}
