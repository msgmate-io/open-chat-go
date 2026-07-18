package database

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func setupToolInitDB(t *testing.T) *DBConfig {
	t.Helper()
	return &DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "tool_init_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
}

func TestDisposeToolInitDataForChatRedactsShapeAndDeletesRows(t *testing.T) {
	dbConfig := setupToolInitDB(t)
	DB := SetupDatabase(*dbConfig)

	user1, err := RegisterUser(DB, "user1", "user1@example.com", []byte("Passw0rd!"))
	if err != nil {
		t.Fatalf("failed to create user1: %v", err)
	}
	user2, err := RegisterUser(DB, "user2", "user2@example.com", []byte("Passw0rd!"))
	if err != nil {
		t.Fatalf("failed to create user2: %v", err)
	}

	chat := Chat{User1Id: user1.ID, User2Id: user2.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	config := map[string]interface{}{
		"tool_init": map[string]interface{}{
			"shared_tool": map[string]interface{}{
				"api_key": "secret-value",
				"nested": map[string]interface{}{
					"token": "nested-secret",
				},
			},
		},
	}
	encodedConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	sharedConfig := SharedChatConfig{ChatId: chat.ID, ConfigData: encodedConfig}
	if err := DB.Create(&sharedConfig).Error; err != nil {
		t.Fatalf("failed to create shared config: %v", err)
	}
	if err := DB.Model(&chat).Update("shared_config_id", sharedConfig.ID).Error; err != nil {
		t.Fatalf("failed to attach shared config: %v", err)
	}

	manager := NewToolInitDataManager(DB)
	if err := manager.StoreToolInitData(chat.ID, "db_tool", map[string]interface{}{
		"session_token": "db-secret",
		"scope":         "full",
	}); err != nil {
		t.Fatalf("failed to store db tool init: %v", err)
	}

	var loadedChat Chat
	if err := DB.Preload("SharedConfig").First(&loadedChat, chat.ID).Error; err != nil {
		t.Fatalf("failed to load chat: %v", err)
	}

	if err := manager.DisposeToolInitDataForChat(&loadedChat); err != nil {
		t.Fatalf("DisposeToolInitDataForChat failed: %v", err)
	}

	var remainingCount int64
	if err := DB.Model(&ToolInitData{}).Where("chat_id = ?", chat.ID).Count(&remainingCount).Error; err != nil {
		t.Fatalf("failed to count tool init rows: %v", err)
	}
	if remainingCount != 0 {
		t.Fatalf("expected 0 tool_init_data rows, got %d", remainingCount)
	}

	var updatedSharedConfig SharedChatConfig
	if err := DB.First(&updatedSharedConfig, sharedConfig.ID).Error; err != nil {
		t.Fatalf("failed to reload shared config: %v", err)
	}

	updatedConfig := map[string]interface{}{}
	if err := json.Unmarshal(updatedSharedConfig.ConfigData, &updatedConfig); err != nil {
		t.Fatalf("failed to decode updated shared config: %v", err)
	}

	toolInitRaw, ok := updatedConfig["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tool_init object in shared config")
	}

	sharedTool, ok := toolInitRaw["shared_tool"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected shared_tool object")
	}
	if value, exists := sharedTool["api_key"]; !exists || value != nil {
		t.Fatalf("expected shared_tool.api_key to be null, got %#v", value)
	}
	nested, ok := sharedTool["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected shared_tool.nested object")
	}
	if value, exists := nested["token"]; !exists || value != nil {
		t.Fatalf("expected shared_tool.nested.token to be null, got %#v", value)
	}

	dbTool, ok := toolInitRaw["db_tool"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected db_tool object")
	}
	if value, exists := dbTool["session_token"]; !exists || value != nil {
		t.Fatalf("expected db_tool.session_token to be null, got %#v", value)
	}
	if value, exists := dbTool["scope"]; !exists || value != nil {
		t.Fatalf("expected db_tool.scope to be null, got %#v", value)
	}
}
