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
