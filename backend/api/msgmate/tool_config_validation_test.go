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
