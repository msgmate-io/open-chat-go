package msgmate

import (
	"strings"
	"testing"
)

func TestValidateToolsAndInitConfigRequiresSeededRandomToolInit(t *testing.T) {
	err := ValidateToolsAndInitConfig([]interface{}{"get_random_number_seeded"}, nil)
	if err == nil {
		t.Fatalf("expected missing tool_init error")
	}
	if !strings.Contains(err.Error(), "missing tool_init for required tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolsAndInitConfigAcceptsSeededRandomToolInit(t *testing.T) {
	err := ValidateToolsAndInitConfig(
		[]interface{}{"get_random_number_seeded"},
		map[string]interface{}{
			"get_random_number_seeded": map[string]interface{}{"seed": float64(12345)},
		},
	)
	if err != nil {
		t.Fatalf("expected valid tool_init, got error: %v", err)
	}
}

func noopInitTool(name string) Tool {
	return &BaseTool{
		ToolName:     name,
		RequiresInit: true,
		ToolInitSchema: map[string]interface{}{
			"type":        "object",
			"properties":  map[string]interface{}{},
			"required":    []string{},
			"description": "No manual tool_init fields are required. Runtime context is injected automatically.",
		},
	}
}

// TestValidateToolsAndInitConfigToleratesNoopRequiredInit mirrors the
// ci.msgmate.io runtime-selector bot: it lists integration tools that declare
// RequiresInit (their runtime context is injected automatically) but have an
// init schema without any required fields. A config delta that adds such a tool
// to `tools` without a matching `tool_init` entry must not be rejected.
func TestValidateToolsAndInitConfigToleratesNoopRequiredInit(t *testing.T) {
	resolver := func(toolName string) (Tool, bool, error) {
		switch toolName {
		case "git_list_pr_sessions", "git_propose_interaction_continuation":
			return noopInitTool(toolName), true, nil
		default:
			return nil, false, nil
		}
	}

	err := ValidateToolsAndInitConfigWithResolver(
		[]interface{}{"git_list_pr_sessions", "git_propose_interaction_continuation"},
		nil,
		resolver,
	)
	if err != nil {
		t.Fatalf("expected no-op required-init tools to be tolerated, got: %v", err)
	}
}

// TestValidateToolsAndInitConfigKeepsRuntimeSelectorChoices ensures the fix
// does not change behaviour for a tool whose tool_init is present (the
// runtime-selector choices must still validate) and that unknown tool_init keys
// are still rejected.
func TestValidateToolsAndInitConfigKeepsRuntimeSelectorChoices(t *testing.T) {
	resolver := func(toolName string) (Tool, bool, error) {
		switch toolName {
		case "git_propose_interaction_runtime":
			return &BaseTool{
				ToolName:     toolName,
				RequiresInit: true,
				ToolInitSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"choices": map[string]interface{}{"type": "array"},
					},
					"required": []string{},
				},
			}, true, nil
		case "git_list_pr_sessions":
			return noopInitTool(toolName), true, nil
		default:
			return nil, false, nil
		}
	}

	err := ValidateToolsAndInitConfigWithResolver(
		[]interface{}{"git_propose_interaction_runtime", "git_list_pr_sessions"},
		map[string]interface{}{
			"git_propose_interaction_runtime": map[string]interface{}{
				"choices": []interface{}{map[string]interface{}{"bot": "issue-repl-bot"}},
			},
		},
		resolver,
	)
	if err != nil {
		t.Fatalf("expected runtime selector config to validate, got: %v", err)
	}

	err = ValidateToolsAndInitConfigWithResolver(
		[]interface{}{"git_list_pr_sessions"},
		map[string]interface{}{"git_unknown_tool": map[string]interface{}{}},
		resolver,
	)
	if err == nil || !strings.Contains(err.Error(), "unknown tool key") {
		t.Fatalf("expected unknown tool_init key error, got: %v", err)
	}
}

// TestValidateToolsAndInitConfigStillRequiresInitWithRequiredFields guards the
// existing contract: a tool whose init schema requires fields must still fail
// when the config omits its tool_init.
func TestValidateToolsAndInitConfigStillRequiresInitWithRequiredFields(t *testing.T) {
	resolver := func(toolName string) (Tool, bool, error) {
		if toolName != "seeded_tool" {
			return nil, false, nil
		}
		return &BaseTool{
			ToolName:     "seeded_tool",
			RequiresInit: true,
			ToolInitSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"seed"},
				"properties": map[string]interface{}{
					"seed": map[string]interface{}{"type": "integer"},
				},
			},
		}, true, nil
	}

	err := ValidateToolsAndInitConfigWithResolver([]interface{}{"seeded_tool"}, nil, resolver)
	if err == nil || !strings.Contains(err.Error(), "missing tool_init for required tool") {
		t.Fatalf("expected missing tool_init error, got: %v", err)
	}
}

func TestValidatePayloadAgainstSchemaSupportsAlternativeRequiredFields(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"project":      map[string]interface{}{"type": "string"},
			"project_uuid": map[string]interface{}{"type": "string"},
		},
		"anyOf": []interface{}{
			map[string]interface{}{"required": []string{"project"}},
			map[string]interface{}{"required": []string{"project_uuid"}},
		},
	}

	for _, payload := range []map[string]interface{}{
		{"project": "open-chat-go"},
		{"project_uuid": "project-uuid"},
	} {
		if err := ValidatePayloadAgainstSchema(payload, schema, true); err != nil {
			t.Fatalf("expected alternative payload %+v to be valid: %v", payload, err)
		}
	}
	if err := ValidatePayloadAgainstSchema(map[string]interface{}{}, schema, true); err == nil {
		t.Fatalf("expected empty payload to fail alternative field validation")
	}
}
