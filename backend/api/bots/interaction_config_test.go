package bots

import "testing"

func TestApplyInteractionConfigOverridesPreservesDefaultToolInitWhenOmitted(t *testing.T) {
	defaultToolInit := map[string]interface{}{
		"opencode_select_project": map[string]interface{}{"project_uuid": "default-project"},
	}
	config := applyInteractionConfigOverrides(
		map[string]interface{}{"tool_init": defaultToolInit, "temperature": 0.7},
		map[string]interface{}{"temperature": 0.1},
		nil,
	)

	if config["tool_init"] == nil {
		t.Fatalf("expected omitted tool_init to preserve bot default")
	}
	if config["temperature"] != 0.1 {
		t.Fatalf("expected regular config override to apply, got %#v", config["temperature"])
	}
}

func TestApplyInteractionConfigOverridesReplacesToolInitWhenProvided(t *testing.T) {
	replacement := map[string]interface{}{}
	config := applyInteractionConfigOverrides(
		map[string]interface{}{
			"tool_init": map[string]interface{}{
				"opencode_select_project": map[string]interface{}{"project_uuid": "default-project"},
			},
		},
		nil,
		replacement,
	)

	actual, ok := config["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tool_init map, got %#v", config["tool_init"])
	}
	if len(actual) != 0 {
		t.Fatalf("expected explicit empty object to clear tool_init, got %#v", actual)
	}
}

func TestApplyInteractionConfigOverridesMergesToolInitWhenProvided(t *testing.T) {
	config := applyInteractionConfigOverrides(
		map[string]interface{}{
			"tool_init": map[string]interface{}{
				"git_propose_interaction_runtime": map[string]interface{}{"choices": []interface{}{"a"}},
				"opencode_select_project":         map[string]interface{}{"project_uuid": "default-project"},
			},
		},
		nil,
		map[string]interface{}{
			"git_pr_start": map[string]interface{}{"workspace_uuid": "ws-1"},
		},
	)

	actual, ok := config["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tool_init map, got %#v", config["tool_init"])
	}
	// Per-interaction keys augment the bot default instead of dropping it, so
	// a required tool_init the bot declares can never be lost.
	if actual["git_propose_interaction_runtime"] == nil {
		t.Fatalf("expected bot default tool_init to be preserved, got %#v", actual)
	}
	if actual["opencode_select_project"] == nil {
		t.Fatalf("expected other bot default tool_init keys to be preserved, got %#v", actual)
	}
	if prStart, ok := actual["git_pr_start"].(map[string]interface{}); !ok || prStart["workspace_uuid"] != "ws-1" {
		t.Fatalf("expected per-interaction tool_init to apply, got %#v", actual["git_pr_start"])
	}
}

func TestApplyInteractionConfigOverridesIgnoresNestedToolInitOverride(t *testing.T) {
	defaultToolInit := map[string]interface{}{"default_tool": map[string]interface{}{}}
	config := applyInteractionConfigOverrides(
		map[string]interface{}{"tool_init": defaultToolInit},
		map[string]interface{}{
			"tool_init": map[string]interface{}{"bypass_tool": map[string]interface{}{}},
		},
		nil,
	)

	actual, ok := config["tool_init"].(map[string]interface{})
	if !ok || actual["default_tool"] == nil || actual["bypass_tool"] != nil {
		t.Fatalf("config_overrides.tool_init must not bypass the dedicated field: %#v", config["tool_init"])
	}
}
