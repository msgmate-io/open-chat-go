package bots

import "testing"

// ci#622 / ci ticket #827: the native-coder selfcheck harness dispatches every
// role with a tool_init override that must replace the coder_select_workspace
// selector wholesale while leaving every other tool_init key of the bot default
// untouched. If this merge regresses, the CI dispatch no longer reaches a
// usable workspace selector.
func TestApplyInteractionConfigOverridesCi622SelectorOverride(t *testing.T) {
	effective := map[string]interface{}{
		"tool_init": map[string]interface{}{
			"coder_select_workspace": map[string]interface{}{
				"auto_sandbox": false,
				"workspace":    "bot-default-workspace",
			},
			"other_tool": map[string]interface{}{"keep": true},
		},
	}
	selector := map[string]interface{}{
		"auto_sandbox": true,
		"git_repo":     "https://github.com/msgmate-io/open-chat-go.git",
		"mode":         "plan",
		"interaction":  "autonomous",
	}
	got := applyInteractionConfigOverrides(effective, nil, map[string]interface{}{
		"coder_select_workspace": selector,
	})

	toolInit, ok := got["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_init missing after merge: %#v", got["tool_init"])
	}
	merged, ok := toolInit["coder_select_workspace"].(map[string]interface{})
	if !ok {
		t.Fatalf("coder_select_workspace missing after merge: %#v", toolInit)
	}
	// The override replaces the whole selector value (no leftover bot default).
	if _, exists := merged["workspace"]; exists {
		t.Fatalf("stale bot-default selector key survived the override: %#v", merged)
	}
	for key, want := range selector {
		if merged[key] != want {
			t.Fatalf("selector key %q = %#v, want %#v", key, merged[key], want)
		}
	}
	// Other tool keys of the bot default are preserved.
	other, ok := toolInit["other_tool"].(map[string]interface{})
	if !ok || other["keep"] != true {
		t.Fatalf("unrelated tool_init key was dropped: %#v", toolInit)
	}
}

// An omitted tool_init must preserve the bot default; an explicit empty object
// clears it (documented way to start without any tool_init).
func TestApplyInteractionConfigOverridesCi622OmittedAndEmpty(t *testing.T) {
	base := map[string]interface{}{"tool_init": map[string]interface{}{"a": "b"}}

	kept := applyInteractionConfigOverrides(map[string]interface{}{"tool_init": map[string]interface{}{"a": "b"}}, nil, nil)
	if _, ok := kept["tool_init"].(map[string]interface{})["a"]; !ok {
		t.Fatalf("omitted tool_init must keep the bot default: %#v", kept)
	}

	cleared := applyInteractionConfigOverrides(base, nil, map[string]interface{}{})
	if raw, ok := cleared["tool_init"].(map[string]interface{}); !ok || len(raw) != 0 {
		t.Fatalf("empty tool_init must clear the default: %#v", cleared["tool_init"])
	}
}
