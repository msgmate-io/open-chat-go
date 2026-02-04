package msgmate

import (
	"encoding/json"
	"testing"
)

// TestGetToolInputSchemaNormalizesRequired guards the OpenRouter strict-schema
// failure `tools.N.function.parameters/required: invalid JSON Schema`: a tool
// with no RequiredParams must serialize `required` as an array, never `null`.
func TestGetToolInputSchemaNormalizesRequired(t *testing.T) {
	tool := &BaseTool{ToolName: "coder_read", ToolType: "function"}
	schema := tool.GetToolInputSchema()

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required must be a []string, got %#v", schema["required"])
	}
	if len(required) != 0 {
		t.Fatalf("expected empty required, got %#v", required)
	}
	if _, ok := schema["properties"].(map[string]interface{}); !ok {
		t.Fatalf("properties must be an object, got %#v", schema["properties"])
	}

	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("schema must marshal: %v", err)
	}
	if string(encoded) == "" {
		t.Fatal("empty schema")
	}
	// The raw JSON must not contain a null required.
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["required"] == nil {
		t.Fatalf("required must not be null in JSON: %s", encoded)
	}
}

// TestNormalizeToolInputSchema cleans a caller-provided schema.
func TestNormalizeToolInputSchema(t *testing.T) {
	normalized := normalizeToolInputSchema(map[string]interface{}{
		"properties": nil,
		"required":   []interface{}{"path", 42, "query"},
	})
	if got, ok := normalized["required"].([]string); !ok || len(got) != 2 || got[0] != "path" || got[1] != "query" {
		t.Fatalf("non-string required entries must be dropped, got %#v", normalized["required"])
	}
	if _, ok := normalized["properties"].(map[string]interface{}); !ok {
		t.Fatalf("nil properties must become an object, got %#v", normalized["properties"])
	}
	if normalized["type"] != "object" {
		t.Fatalf("missing type must default to object, got %#v", normalized["type"])
	}
}

// TestConstructToolParametersAreValid confirms the constructed OpenAI function
// schema always carries an array `required`.
func TestConstructToolParametersAreValid(t *testing.T) {
	tool := &BaseTool{ToolName: "noop", ToolType: "function", ToolDescription: "d"}
	constructed, ok := tool.ConstructTool().(map[string]interface{})
	if !ok {
		t.Fatal("ConstructTool must return a map")
	}
	fn, ok := constructed["function"].(map[string]interface{})
	if !ok {
		t.Fatal("function must be a map")
	}
	params, ok := fn["parameters"].(map[string]interface{})
	if !ok {
		t.Fatal("parameters must be a map")
	}
	if _, ok := params["required"].([]string); !ok {
		t.Fatalf("parameters.required must be a []string, got %#v", params["required"])
	}
}
