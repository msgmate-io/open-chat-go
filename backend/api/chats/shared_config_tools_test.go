package chats

import "testing"

// TestApplySharedConfigDefaultsDropsEmptyTools guards the ci#617 fix on the
// chat-creation path: a config with `tools: []` must not keep an empty list
// (which suppresses every compiled-in integration default).
func TestApplySharedConfigDefaultsDropsEmptyTools(t *testing.T) {
	applied := applySharedConfigDefaults(map[string]interface{}{
		"model":   "some-model",
		"backend": "litellm",
		"tools":   []interface{}{},
	})
	if raw, present := applied["tools"]; present {
		list, isList := raw.([]interface{})
		if !isList || len(list) == 0 {
			t.Fatalf("expected the empty tools list to be replaced by defaults, got %#v", raw)
		}
	}

	narrowed := applySharedConfigDefaults(map[string]interface{}{
		"model": "some-model",
		"tools": []interface{}{"coder_read"},
	})
	names, ok := narrowed["tools"].([]interface{})
	if !ok || len(names) != 1 || names[0] != "coder_read" {
		t.Fatalf("expected the narrowed list to survive, got %#v", narrowed["tools"])
	}
}
