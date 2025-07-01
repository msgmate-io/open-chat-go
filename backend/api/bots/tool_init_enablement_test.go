package bots

import (
	"backend/database"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// ensureDynamicRESTToolsTable migrates the dynamic_rest_tools table, which is
// normally registered by the REST tool integration at server startup.
func ensureDynamicRESTToolsTable(t *testing.T, DB *gorm.DB) {
	t.Helper()
	if err := DB.AutoMigrate(&database.DynamicRESTTool{}); err != nil {
		t.Fatalf("failed to migrate dynamic rest tools table: %v", err)
	}
}

func createDynamicRESTToolForTest(t *testing.T, DB *gorm.DB, owner *database.User, name string) {
	t.Helper()

	ensureDynamicRESTToolsTable(t, DB)

	openAPISource := fmt.Sprintf(`{
		"openapi":"3.0.3",
		"info":{"title":"Test API","version":"1.0.0"},
		"servers":[{"url":"http://test.example"}],
		"paths":{
			"/ping":{
				"get":{
					"operationId":"getPing",
					"responses":{"200":{"description":"ok"}}
				}
			}
		}
	}`)

	row := database.DynamicRESTTool{
		OwnerUserId:       owner.ID,
		Name:              name,
		Description:       "test dynamic rest tool",
		Enabled:           true,
		OpenAPISourceType: "inline",
		OpenAPISource:     openAPISource,
		OperationID:       "getPing",
		ParamBindings:     json.RawMessage("[]"),
		SafetyPolicy:      json.RawMessage(`{"allow_private_ips": true}`),
	}
	if err := DB.Create(&row).Error; err != nil {
		t.Fatalf("failed to create dynamic rest tool: %v", err)
	}
}

func configuredToolNames(t *testing.T, config map[string]interface{}) []string {
	t.Helper()
	names, err := collectToolNames(config["tools"])
	if err != nil {
		t.Fatalf("failed to collect tool names: %v", err)
	}
	return names
}

func TestToolInitImplicitlyEnablesDynamicTools(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "tool-init-owner@example.com", false)
	createDynamicRESTToolForTest(t, DB, owner, "little_world_chat_reply")

	config := map[string]interface{}{
		"tools": []interface{}{},
		"tool_init": map[string]interface{}{
			"little_world_chat_reply": map[string]interface{}{"api_host": "http://test.example"},
		},
	}

	if err := validateAndAttachDynamicToolsForUser(DB, owner, config); err != nil {
		t.Fatalf("expected tool_init to implicitly enable resolvable dynamic tools, got error: %v", err)
	}

	names := configuredToolNames(t, config)
	found := false
	for _, name := range names {
		if name == "little_world_chat_reply" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected implicitly enabled tool in config tools, got %v", names)
	}

	dynamicTools, ok := config["dynamic_tools"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected dynamic_tools snapshot to be attached, got %T", config["dynamic_tools"])
	}
	if _, exists := dynamicTools["little_world_chat_reply"]; !exists {
		t.Fatalf("expected dynamic_tools snapshot for implicitly enabled tool, got keys %v", dynamicTools)
	}
}

func TestToolInitUnknownKeyStillRejected(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "tool-init-unknown@example.com", false)
	ensureDynamicRESTToolsTable(t, DB)

	config := map[string]interface{}{
		"tools": []interface{}{},
		"tool_init": map[string]interface{}{
			"does_not_exist_tool": map[string]interface{}{"api_host": "http://test.example"},
		},
	}

	err := validateAndAttachDynamicToolsForUser(DB, owner, config)
	if err == nil {
		t.Fatalf("expected error for unresolvable tool_init key")
	}
	if !strings.Contains(err.Error(), "does_not_exist_tool") {
		t.Fatalf("expected error to reference the unknown tool key, got: %v", err)
	}
}

func TestToolInitDoesNotDuplicateConfiguredTools(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "tool-init-configured@example.com", false)
	createDynamicRESTToolForTest(t, DB, owner, "little_world_chat_reply")

	config := map[string]interface{}{
		"tools": []interface{}{"little_world_chat_reply"},
		"tool_init": map[string]interface{}{
			"little_world_chat_reply": map[string]interface{}{"api_host": "http://test.example"},
		},
	}

	if err := validateAndAttachDynamicToolsForUser(DB, owner, config); err != nil {
		t.Fatalf("expected validation to pass for configured tool, got error: %v", err)
	}

	names := configuredToolNames(t, config)
	count := 0
	for _, name := range names {
		if name == "little_world_chat_reply" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected tool to appear exactly once, got %d in %v", count, names)
	}
}
