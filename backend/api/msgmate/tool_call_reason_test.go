package msgmate

import "testing"

func TestExtractToolCallReason(t *testing.T) {
	cases := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"reason", map[string]interface{}{"reason": "check the build"}, "check the build"},
		{"call_reason alias", map[string]interface{}{"call_reason": "why not"}, "why not"},
		{"why alias", map[string]interface{}{"why": "verify"}, "verify"},
		{"preferred key wins", map[string]interface{}{"reason": "a", "call_reason": "b"}, "a"},
		{"raw json string", `{"reason":"from json"}`, "from json"},
		{"missing", map[string]interface{}{"path": "x"}, ""},
		{"blank", map[string]interface{}{"reason": "   "}, ""},
		{"non string", map[string]interface{}{"reason": 42}, ""},
		{"nil input", nil, ""},
		{"unparseable string", "not json", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractToolCallReason(tc.input); got != tc.want {
				t.Fatalf("ExtractToolCallReason(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractToolCallReasonTruncates(t *testing.T) {
	long := make([]rune, ToolCallReasonMaxChars+50)
	for i := range long {
		long[i] = 'x'
	}
	got := ExtractToolCallReason(map[string]interface{}{"reason": string(long)})
	if len([]rune(got)) != ToolCallReasonMaxChars+1 {
		t.Fatalf("expected truncation to %d runes plus ellipsis, got %d", ToolCallReasonMaxChars, len([]rune(got)))
	}
}

func TestWithToolCallReasonProperty(t *testing.T) {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
		"required":   []string{"path"},
	}
	got := withToolCallReasonProperty(schema)
	props := got["properties"].(map[string]interface{})
	if _, ok := props["reason"]; !ok {
		t.Fatalf("reason property missing: %#v", props)
	}
	if props["path"] == nil {
		t.Fatalf("existing property dropped: %#v", props)
	}
	if reqs, _ := got["required"].([]string); len(reqs) != 1 || reqs[0] != "path" {
		t.Fatalf("reason must not become required: %#v", got["required"])
	}

	// Calling twice must not duplicate or replace an existing property.
	existing := map[string]interface{}{
		"properties": map[string]interface{}{"reason": map[string]interface{}{"type": "string", "description": "custom"}},
	}
	twice := withToolCallReasonProperty(withToolCallReasonProperty(existing))
	if d := twice["properties"].(map[string]interface{})["reason"].(map[string]interface{})["description"]; d != "custom" {
		t.Fatalf("existing reason property was overwritten: %v", d)
	}
}

func TestGetToolInputSchemaAdvertisesReasonForCoderToolsOnly(t *testing.T) {
	coder := NewToolFromDefinition(ToolDefinition{Name: "coder_bash", FunctionName: "coder_bash", InputType: map[string]interface{}{}})
	props := coder.GetToolInputSchema()["properties"].(map[string]interface{})
	if _, ok := props["reason"]; !ok {
		t.Fatalf("coder tool schema missing reason: %#v", props)
	}

	other := NewToolFromDefinition(ToolDefinition{Name: "web_search", FunctionName: "web_search", InputType: map[string]interface{}{}})
	otherProps := other.GetToolInputSchema()["properties"].(map[string]interface{})
	if _, ok := otherProps["reason"]; ok {
		t.Fatalf("non-coder tool schema must not advertise reason: %#v", otherProps)
	}
}

func TestIsCoderToolName(t *testing.T) {
	if !IsCoderToolName("coder_read") || IsCoderToolName("grep") || IsCoderToolName("") {
		t.Fatal("unexpected coder tool name classification")
	}
}
