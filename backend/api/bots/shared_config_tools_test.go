package bots

import "testing"

// TestApplyIntegrationDefaultsDropsEmptyTools guards the ci#617 fix on the
// bot-config path: a stored config with `tools: []` must not come back as an
// empty list (which suppresses every integration default), while an explicitly
// narrowed list is preserved verbatim.
func TestApplyIntegrationDefaultsDropsEmptyTools(t *testing.T) {
	empty := map[string]interface{}{
		"model":   "some-model",
		"backend": "litellm",
		"tools":   []interface{}{},
	}
	applied, err := applyIntegrationDefaultsForUser(nil, nil, empty)
	if err != nil {
		t.Fatalf("defaults failed: %v", err)
	}
	if raw, present := applied["tools"]; present {
		list, isList := raw.([]interface{})
		if !isList || len(list) == 0 {
			t.Fatalf("expected the empty tools list to be replaced by defaults, got %#v", raw)
		}
	}

	narrowed := map[string]interface{}{
		"model":             "some-model",
		"backend":           "litellm",
		"coder_interaction": "autonomous",
		"tools":             []interface{}{"coder_read"},
	}
	appliedNarrowed, err := applyIntegrationDefaultsForUser(nil, nil, narrowed)
	if err != nil {
		t.Fatalf("defaults failed: %v", err)
	}
	names, ok := appliedNarrowed["tools"].([]interface{})
	if !ok || len(names) != 1 || names[0] != "coder_read" {
		t.Fatalf("expected the narrowed list to survive, got %#v", appliedNarrowed["tools"])
	}
}
