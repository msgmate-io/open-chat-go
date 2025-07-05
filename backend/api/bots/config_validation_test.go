package bots

import (
	"backend/integrations"
	"strings"
	"testing"
)

func TestValidateSharedConfigStructureValid(t *testing.T) {
	config := map[string]interface{}{
		"model":       "qwen3-4b-instruct-2507_vllm",
		"backend":     "litellm",
		"endpoint":    "https://litellm.t1m.me/v1",
		"temperature": 0.0,
		"max_tokens":  8000.0,
		"context":     8000.0,
		"tools":       []interface{}{"get_weather"},
	}

	if err := validateSharedConfigStructure(config); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestValidateSharedConfigStructureMissingRequiredKeys(t *testing.T) {
	config := map[string]interface{}{
		"backend": "litellm",
	}

	err := validateSharedConfigStructure(config)
	if err == nil {
		t.Fatalf("expected error for missing model")
	}
}

func TestValidateSharedConfigStructureRejectsInvalidToolsType(t *testing.T) {
	config := map[string]interface{}{
		"model":   "qwen3-4b-instruct-2507_vllm",
		"backend": "litellm",
		"tools":   []interface{}{true},
	}

	err := validateSharedConfigStructure(config)
	if err == nil {
		t.Fatalf("expected error for non-string tool entry")
	}
}

func TestValidateSharedConfigStructureAcceptsToolCallLimitOverrides(t *testing.T) {
	config := map[string]interface{}{
		"model":                "qwen3-4b-instruct-2507_vllm",
		"backend":              "litellm",
		"tool_call_max_total":  24.0,
		"tool_call_max_failed": 5.0,
	}

	if err := validateSharedConfigStructure(config); err != nil {
		t.Fatalf("expected valid tool call limits, got error: %v", err)
	}
}

func TestValidateSharedConfigStructureValidatesMaxCompletionTokensOverride(t *testing.T) {
	validConfig := map[string]interface{}{
		"model":                     "company-reasoning-model",
		"backend":                   "openai",
		"use_max_completion_tokens": true,
		"disabled_sampling_params":  []interface{}{"temperature", "top_p"},
	}
	if err := validateSharedConfigStructure(validConfig); err != nil {
		t.Fatalf("expected boolean use_max_completion_tokens override to be valid, got error: %v", err)
	}

	invalidConfig := map[string]interface{}{
		"model":                     "company-reasoning-model",
		"backend":                   "openai",
		"use_max_completion_tokens": "true",
	}
	err := validateSharedConfigStructure(invalidConfig)
	if err == nil || !strings.Contains(err.Error(), "use_max_completion_tokens must be a boolean") {
		t.Fatalf("expected invalid override type error, got %v", err)
	}

	invalidConfig = map[string]interface{}{
		"model":                    "company-reasoning-model",
		"backend":                  "openai",
		"disabled_sampling_params": []interface{}{"temperature", "unknown_parameter"},
	}
	err = validateSharedConfigStructure(invalidConfig)
	if err == nil || !strings.Contains(err.Error(), "unsupported parameter") {
		t.Fatalf("expected unsupported disabled sampling parameter error, got %v", err)
	}
}

func TestValidateSharedConfigStructureRejectsInvalidToolCallLimitValues(t *testing.T) {
	config := map[string]interface{}{
		"model":                "qwen3-4b-instruct-2507_vllm",
		"backend":              "litellm",
		"tool_call_max_total":  0.0,
		"tool_call_max_failed": -1.0,
	}

	err := validateSharedConfigStructure(config)
	if err == nil {
		t.Fatalf("expected error for invalid tool call limits")
	}
}

func TestValidateAndAttachMCPIntegrationsForUserIgnoresBuiltInIntegrationNames(t *testing.T) {
	if !integrations.Has("mcp") {
		t.Skip("mcp integration not available in current build")
	}

	DB := setupBotsTestDB(t)
	user := createUserForBotsTest(t, DB, "owner@example.com", false)
	config := map[string]interface{}{
		"integrations": []interface{}{"ssh"},
		"tools":        []interface{}{"ssh_list_accessible_servers", "mcp:ssh:list"},
	}

	if err := validateAndAttachMCPIntegrationsForUser(DB, user, config); err != nil {
		t.Fatalf("expected built-in integration names to be ignored, got error: %v", err)
	}

	if _, exists := config["integrations"]; exists {
		t.Fatalf("expected integrations list to be removed when no MCP integrations are active")
	}

	tools, err := collectToolNames(config["tools"])
	if err != nil {
		t.Fatalf("expected valid tools after normalization: %v", err)
	}
	for _, toolName := range tools {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(toolName)), "mcp:") {
			t.Fatalf("expected mcp-prefixed tools to be removed when no MCP integrations are active")
		}
	}
}
