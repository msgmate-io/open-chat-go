package cmd

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupBotConfigTestDB(t *testing.T) *database.DBConfig {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "bot_config_test.db")
	config := &database.DBConfig{
		Backend:  "sqlite",
		FilePath: dbPath,
		Debug:    false,
		ResetDB:  true,
	}
	return config
}

func writeBotConfigFile(t *testing.T, payload map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bot.json")
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal bot payload: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("failed to write bot config file: %v", err)
	}
	return path
}

func TestApplyBotBootstrapConfigFilesCreatesRuntimeAndContact(t *testing.T) {
	config := setupBotConfigTestDB(t)
	DB := database.SetupDatabase(*config)

	if err, _ := util.CreateUser(DB, "owner_user", "OwnerPass1!", false); err != nil {
		t.Fatalf("failed to create owner user: %v", err)
	}

	path := writeBotConfigFile(t, map[string]interface{}{
		"owner": map[string]interface{}{
			"username": "owner_user",
		},
		"bot": map[string]interface{}{
			"username":    "owner_support_bot",
			"password":    "BotPass1!",
			"name":        "support_bot",
			"description": "Support bot",
			"is_public":   false,
			"is_active":   true,
		},
		"default_shared_config": map[string]interface{}{
			"model":       "qwen3-8b-instruct_vllm",
			"temperature": 0.2,
		},
	})

	if err := applyBotBootstrapConfigFiles(DB, []string{path}, false); err != nil {
		t.Fatalf("applyBotBootstrapConfigFiles failed: %v", err)
	}

	owner, err := findUserByUsername(DB, "owner_user")
	if err != nil {
		t.Fatalf("failed to resolve owner user: %v", err)
	}

	botUser, err := findUserByUsername(DB, "owner_support_bot")
	if err != nil {
		t.Fatalf("failed to resolve bot user: %v", err)
	}
	if !botUser.IsAutomated {
		t.Fatalf("expected bot user to be automated")
	}

	var runtime database.BotRuntimeConfig
	if err := DB.Where("owner_user_id = ? AND name = ?", owner.ID, "support_bot").First(&runtime).Error; err != nil {
		t.Fatalf("failed to load bot runtime config: %v", err)
	}
	if runtime.BotUserId != botUser.ID {
		t.Fatalf("expected runtime bot_user_id=%d, got %d", botUser.ID, runtime.BotUserId)
	}

	var shared map[string]interface{}
	if err := json.Unmarshal(runtime.DefaultSharedConfig, &shared); err != nil {
		t.Fatalf("failed to decode default_shared_config: %v", err)
	}
	if shared["model"] != "qwen3-8b-instruct_vllm" {
		t.Fatalf("unexpected model in default_shared_config: %v", shared["model"])
	}

	var contact database.Contact
	if err := DB.Where("owning_user_id = ? AND contact_user_id = ?", owner.ID, botUser.ID).First(&contact).Error; err != nil {
		t.Fatalf("expected owner->bot contact to exist: %v", err)
	}
}

func TestApplyBotBootstrapConfigFilesRequiresOwner(t *testing.T) {
	config := setupBotConfigTestDB(t)
	DB := database.SetupDatabase(*config)

	path := writeBotConfigFile(t, map[string]interface{}{
		"owner": map[string]interface{}{
			"username": "missing_owner",
		},
		"bot": map[string]interface{}{
			"username": "orphan_bot",
			"password": "BotPass1!",
			"name":     "orphan_bot",
		},
		"default_shared_config": map[string]interface{}{
			"model": "qwen3-8b-instruct_vllm",
		},
	})

	err := applyBotBootstrapConfigFiles(DB, []string{path}, false)
	if err == nil {
		t.Fatalf("expected missing owner error")
	}
	if !strings.Contains(err.Error(), "owner user must already exist") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyBotBootstrapConfigFilesIsIdempotent(t *testing.T) {
	config := setupBotConfigTestDB(t)
	DB := database.SetupDatabase(*config)

	if err, _ := util.CreateUser(DB, "owner_user", "OwnerPass1!", false); err != nil {
		t.Fatalf("failed to create owner user: %v", err)
	}

	path := writeBotConfigFile(t, map[string]interface{}{
		"owner": map[string]interface{}{
			"username": "owner_user",
		},
		"bot": map[string]interface{}{
			"username": "owner_support_bot",
			"password": "BotPass1!",
			"name":     "support_bot",
		},
		"default_shared_config": map[string]interface{}{
			"model": "qwen3-8b-instruct_vllm",
		},
	})

	if err := applyBotBootstrapConfigFiles(DB, []string{path}, false); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if err := applyBotBootstrapConfigFiles(DB, []string{path}, false); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	owner, err := findUserByUsername(DB, "owner_user")
	if err != nil {
		t.Fatalf("failed to resolve owner user: %v", err)
	}
	bot, err := findUserByUsername(DB, "owner_support_bot")
	if err != nil {
		t.Fatalf("failed to resolve bot user: %v", err)
	}

	var runtimeCount int64
	DB.Model(&database.BotRuntimeConfig{}).Where("owner_user_id = ? AND name = ?", owner.ID, "support_bot").Count(&runtimeCount)
	if runtimeCount != 1 {
		t.Fatalf("expected one runtime config row, got %d", runtimeCount)
	}

	var contactCount int64
	DB.Model(&database.Contact{}).Where("owning_user_id = ? AND contact_user_id = ?", owner.ID, bot.ID).Count(&contactCount)
	if contactCount != 1 {
		t.Fatalf("expected one owner->bot contact row, got %d", contactCount)
	}
}

func TestApplyBotBootstrapConfigFilesDoesNotOverwriteExistingRuntime(t *testing.T) {
	config := setupBotConfigTestDB(t)
	DB := database.SetupDatabase(*config)

	if err, _ := util.CreateUser(DB, "owner_user", "OwnerPass1!", false); err != nil {
		t.Fatalf("failed to create owner user: %v", err)
	}

	initialPath := writeBotConfigFile(t, map[string]interface{}{
		"owner": map[string]interface{}{
			"username": "owner_user",
		},
		"bot": map[string]interface{}{
			"username":    "owner_support_bot",
			"password":    "BotPass1!",
			"name":        "support_bot",
			"description": "Initial description",
			"is_public":   false,
			"is_active":   true,
		},
		"default_shared_config": map[string]interface{}{
			"model": "qwen3-8b-instruct_vllm",
		},
	})

	overwriteAttemptPath := writeBotConfigFile(t, map[string]interface{}{
		"owner": map[string]interface{}{
			"username": "owner_user",
		},
		"bot": map[string]interface{}{
			"username":    "owner_support_bot",
			"password":    "BotPass1!",
			"name":        "support_bot",
			"description": "Overwritten description",
			"is_public":   true,
			"is_active":   false,
		},
		"default_shared_config": map[string]interface{}{
			"model": "gpt-4.1",
		},
	})

	if err := applyBotBootstrapConfigFiles(DB, []string{initialPath}, false); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if err := applyBotBootstrapConfigFiles(DB, []string{overwriteAttemptPath}, false); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	owner, err := findUserByUsername(DB, "owner_user")
	if err != nil {
		t.Fatalf("failed to resolve owner user: %v", err)
	}

	var runtime database.BotRuntimeConfig
	if err := DB.Where("owner_user_id = ? AND name = ?", owner.ID, "support_bot").First(&runtime).Error; err != nil {
		t.Fatalf("failed to load bot runtime config: %v", err)
	}

	if runtime.Description != "Initial description" {
		t.Fatalf("expected original description to be preserved, got %q", runtime.Description)
	}
	if runtime.IsPublic {
		t.Fatalf("expected original is_public=false to be preserved")
	}
	if !runtime.IsActive {
		t.Fatalf("expected original is_active=true to be preserved")
	}

	var shared map[string]interface{}
	if err := json.Unmarshal(runtime.DefaultSharedConfig, &shared); err != nil {
		t.Fatalf("failed to decode default_shared_config: %v", err)
	}
	if shared["model"] != "qwen3-8b-instruct_vllm" {
		t.Fatalf("expected original model to be preserved, got %v", shared["model"])
	}
}

func TestApplyBotBootstrapConfigFilesAllowsMissingPasswordForExistingBot(t *testing.T) {
	config := setupBotConfigTestDB(t)
	DB := database.SetupDatabase(*config)

	if err, _ := util.CreateUser(DB, "owner_user", "OwnerPass1!", false); err != nil {
		t.Fatalf("failed to create owner user: %v", err)
	}
	if err, bot := util.CreateUser(DB, "existing_bot_user", "BotPass1!", false); err != nil {
		t.Fatalf("failed to create existing bot user: %v", err)
	} else {
		bot.IsAutomated = false
		if saveErr := DB.Save(bot).Error; saveErr != nil {
			t.Fatalf("failed to update existing bot user: %v", saveErr)
		}
	}

	path := writeBotConfigFile(t, map[string]interface{}{
		"owner": map[string]interface{}{
			"username": "owner_user",
		},
		"bot": map[string]interface{}{
			"username": "existing_bot_user",
			"name":     "support_bot",
		},
		"default_shared_config": map[string]interface{}{
			"model": "qwen3-8b-instruct_vllm",
		},
	})

	if err := applyBotBootstrapConfigFiles(DB, []string{path}, false); err != nil {
		t.Fatalf("applyBotBootstrapConfigFiles failed: %v", err)
	}

	bot, err := findUserByUsername(DB, "existing_bot_user")
	if err != nil {
		t.Fatalf("failed to resolve bot user: %v", err)
	}
	if !bot.IsAutomated {
		t.Fatalf("expected existing bot user to be marked automated")
	}
}

func TestApplyBotBootstrapConfigFilesMissingPasswordRequiresExistingBot(t *testing.T) {
	config := setupBotConfigTestDB(t)
	DB := database.SetupDatabase(*config)

	if err, _ := util.CreateUser(DB, "owner_user", "OwnerPass1!", false); err != nil {
		t.Fatalf("failed to create owner user: %v", err)
	}

	path := writeBotConfigFile(t, map[string]interface{}{
		"owner": map[string]interface{}{
			"username": "owner_user",
		},
		"bot": map[string]interface{}{
			"username": "missing_bot_user",
			"name":     "support_bot",
		},
		"default_shared_config": map[string]interface{}{
			"model": "qwen3-8b-instruct_vllm",
		},
	})

	err := applyBotBootstrapConfigFiles(DB, []string{path}, false)
	if err == nil {
		t.Fatalf("expected missing existing bot error")
	}
	if !strings.Contains(err.Error(), "bot.password omitted") {
		t.Fatalf("unexpected error: %v", err)
	}
}
