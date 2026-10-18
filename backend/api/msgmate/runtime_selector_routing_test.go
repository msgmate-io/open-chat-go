package msgmate

import "testing"

// TestNormalizeRuntimeSelectorPayloadChoicesRoutesLegacyToNative ensures a
// selector that only offers opencode-era agents is rewritten to offer the
// native msgmate-coder runtime as the recommended default while keeping the
// original name for audit/rollback (ci#621).
func TestNormalizeRuntimeSelectorPayloadChoicesRoutesLegacyToNative(t *testing.T) {
	payload := map[string]interface{}{
		"type": "runtime-selector",
		"choices": []interface{}{
			map[string]interface{}{"bot": "issue-plan-bot", "label": "Plan"},
			map[string]interface{}{"bot": "some-other-runtime", "label": "other"},
		},
	}
	normalizeRuntimeSelectorPayloadChoices(payload)

	choices, _ := payload["choices"].([]interface{})
	if len(choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(choices))
	}
	first := choices[0].(map[string]interface{})
	if got := first["bot"]; got != NativeRuntimePlanBot {
		t.Fatalf("expected native plan runtime first, got %v", got)
	}
	if first["recommended"] != true {
		t.Fatalf("expected the native choice to be recommended, got %v", first["recommended"])
	}
	if first["legacy_bot"] != "issue-plan-bot" {
		t.Fatalf("expected legacy bot preserved, got %v", first["legacy_bot"])
	}
	second := choices[1].(map[string]interface{})
	if second["fallback"] != true {
		t.Fatalf("expected non-native runtime to be a fallback, got %v", second["fallback"])
	}
	if payload["native_runtime_by_default"] != true {
		t.Fatalf("expected native_runtime_by_default, got %v", payload["native_runtime_by_default"])
	}
}

// TestNormalizeRuntimeSelectorPayloadChoicesIsIdempotent ensures repeated
// normalization (eg a partial and a final delivery of the same message) keeps a
// stable choice order and does not stack flags.
func TestNormalizeRuntimeSelectorPayloadChoicesIsIdempotent(t *testing.T) {
	payload := map[string]interface{}{
		"type": "runtime-selector",
		"choices": []interface{}{
			map[string]interface{}{"bot": "opencode"},
			map[string]interface{}{"bot": "coding-bot"},
		},
	}
	normalizeRuntimeSelectorPayloadChoices(payload)
	first, _ := payload["choices"].([]interface{})
	firstBots := make([]string, 0, len(first))
	for _, raw := range first {
		firstBots = append(firstBots, raw.(map[string]interface{})["bot"].(string))
	}

	normalizeRuntimeSelectorPayloadChoices(payload)
	second, _ := payload["choices"].([]interface{})
	if len(second) != len(first) {
		t.Fatalf("choice count changed on second pass: %d != %d", len(second), len(first))
	}
	for i := range second {
		choice := second[i].(map[string]interface{})
		if choice["bot"] != firstBots[i] {
			t.Fatalf("choice order changed on second pass at %d: %v", i, choice["bot"])
		}
		if choice["legacy_bot"] == choice["bot"] {
			t.Fatalf("legacy_bot must not overwrite the native bot at %d", i)
		}
	}
}

// TestRuntimeSelectorChoiceBotName ensures the chosen runtime routes to the
// native bot even when the selector still names a legacy agent.
func TestRuntimeSelectorChoiceBotName(t *testing.T) {
	cases := []struct {
		choice map[string]interface{}
		want   string
	}{
		{map[string]interface{}{"bot": "issue-repl-bot"}, NativeRuntimeReplBot},
		{map[string]interface{}{"bot": "open-chat-development-lead"}, NativeRuntimeLeadBot},
		{map[string]interface{}{"bot": "msgmate-coder-plan-bot"}, NativeRuntimePlanBot},
		{map[string]interface{}{"bot": "an-unrelated-bot"}, "an-unrelated-bot"},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := RuntimeSelectorChoiceBotName(tc.choice); got != tc.want {
			t.Fatalf("RuntimeSelectorChoiceBotName(%v) = %q, want %q", tc.choice, got, tc.want)
		}
	}
}

// TestLegacyRuntimeBotAliasesTargetNativeRuntimes guards the alias map itself:
// every target must be a native msgmate-coder runtime, so a typo'd constant
// (ci#621 shipped `NativeRuntimeRevwBot`, which only failed in the frozen
// workspace build) is caught by the ordinary package test run.
func TestLegacyRuntimeBotAliasesTargetNativeRuntimes(t *testing.T) {
	if len(legacyRuntimeBotAliases) == 0 {
		t.Fatal("legacyRuntimeBotAliases is empty")
	}
	for legacy, native := range legacyRuntimeBotAliases {
		if !IsNativeRuntimeBot(native) {
			t.Errorf("alias %q maps to %q, which is not a native runtime", legacy, native)
		}
		if built, ok := NativeRuntimeBotAlias(legacy); !ok || built != native {
			t.Errorf("NativeRuntimeBotAlias(%q) = (%q, %v), want (%q, true)", legacy, built, ok, native)
		}
	}
}

// TestIsNativeRuntimeBot guards the routing predicate.
func TestIsNativeRuntimeBot(t *testing.T) {
	if !IsNativeRuntimeBot(NativeRuntimeReplBot) {
		t.Fatal("expected msgmate-coder-repl-bot to be native")
	}
	if IsNativeRuntimeBot("opencode") {
		t.Fatal("expected opencode not to be native")
	}
}
