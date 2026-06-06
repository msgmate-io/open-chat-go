package nativecoder

import "testing"

// ci#741 (parent ci#622, failure mode ci#506): the native-coder self-check
// dispatcher states the interaction mode on the coder session it asks the core
// to create (tool_init.coder_select_workspace.interaction = "autonomous")
// instead of on the chat's own dispatch bookkeeping. The core has to read that
// declaration too - otherwise the chat keeps its per-action gate and a headless
// interaction parks on a confirmation instead of running autonomously, which is
// exactly the `development-lead` failure in ci#622.
func TestCoderInteractionFromSelectorToolInit(t *testing.T) {
	config := map[string]interface{}{
		"model": "m",
		"tool_init": map[string]interface{}{
			"coder_select_workspace": map[string]interface{}{
				"auto_sandbox": true,
				"git_repo":     "https://github.com/msgmate-io/native-coder-fixture.git",
				"mode":         "build",
				"interaction":  "autonomous",
			},
		},
	}
	if got := CoderInteractionFromConfig(config); got != CoderInteractionAutonomous {
		t.Fatalf("expected the workspace selector to resolve the interaction as autonomous, got %q", got)
	}
}

// An explicit chat-level mode always wins over the selector declaration,
// including an explicit `interactive` that deliberately keeps the gate.
func TestCoderInteractionChatLevelWinsOverSelector(t *testing.T) {
	config := map[string]interface{}{
		CoderInteractionConfigKey: CoderInteractionInteractive,
		"tool_init": map[string]interface{}{
			"coder_select_workspace": map[string]interface{}{"interaction": "autonomous"},
		},
	}
	if got := CoderInteractionFromConfig(config); got != CoderInteractionInteractive {
		t.Fatalf("expected the explicit chat-level mode to win, got %q", got)
	}
}

// A chat without a native coder session, or whose selector does not declare an
// interaction, stays interactive.
func TestCoderInteractionWithoutSelectorIsInteractive(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"nil config": nil,
		"no tool_init": {
			"model": "m",
		},
		"selector without interaction": {
			"tool_init": map[string]interface{}{
				"coder_select_workspace": map[string]interface{}{"mode": "build"},
			},
		},
		"unrelated tool_init": {
			"tool_init": map[string]interface{}{"some_other_tool": map[string]interface{}{}},
		},
		"malformed selector": {
			"tool_init": map[string]interface{}{"coder_select_workspace": "not-an-object"},
		},
	}
	for name, config := range cases {
		if got := CoderInteractionFromConfig(config); got != CoderInteractionInteractive {
			t.Fatalf("%s: expected interactive, got %q", name, got)
		}
	}
}
