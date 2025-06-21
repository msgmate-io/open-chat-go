package bots

import "testing"

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
