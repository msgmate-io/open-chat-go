package bots

import "testing"

func TestValidateSharedConfigStructureValid(t *testing.T) {
	config := map[string]interface{}{
		"model":       "qwen3-8b-instruct_vllm",
		"backend":     "litellm",
		"endpoint":    "https://litellm.t1m.me/v1",
		"temperature": 0.0,
		"max_tokens":  8000.0,
		"context":     8000.0,
		"tools":       []interface{}{"little_world__chat_reply"},
		"tool_init": map[string]interface{}{
			"little_world__chat_reply": map[string]interface{}{
				"session_id": "s",
				"csrf_token": "c",
				"api_host":   "https://example.com",
				"chat_uuid":  "abc",
			},
		},
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
		"model":   "qwen3-8b-instruct_vllm",
		"backend": "litellm",
		"tools":   []interface{}{true},
	}

	err := validateSharedConfigStructure(config)
	if err == nil {
		t.Fatalf("expected error for non-string tool entry")
	}
}
