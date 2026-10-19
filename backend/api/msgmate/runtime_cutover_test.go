package msgmate

import "testing"

// TestRuntimeCutoverModeDefaultsToFallback guards the ci#584 rollout default:
// an unset or nonsensical NATIVE_CODER_CUTOVER must keep opencode configured as
// the immediate rollback instead of silently retiring it.
func TestRuntimeCutoverModeDefaultsToFallback(t *testing.T) {
	cases := map[string]string{
		"":                CutoverModeFallback,
		"   ":             CutoverModeFallback,
		"garbage":         CutoverModeFallback,
		"FALLBACK":        CutoverModeFallback,
		" native ":        CutoverModeNative,
		"NATIVE":          CutoverModeNative,
		"retired":         CutoverModeRetired,
		"Retired":         CutoverModeRetired,
		"opencode":        CutoverModeFallback,
		"msgmate-coder":   CutoverModeFallback,
		"native;retired":  CutoverModeFallback,
		"fallback,native": CutoverModeFallback,
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(RuntimeCutoverEnvKey, raw)
			if got := RuntimeCutoverMode(); got != want {
				t.Fatalf("RuntimeCutoverMode() with %q = %q, want %q", raw, got, want)
			}
		})
	}
}

// TestRuntimeCutoverStageFlags ensures the two predicates used by the routing
// code stay complementary: only the retirement stage stops offering opencode,
// and only the fallback stage still offers it as a rollback.
func TestRuntimeCutoverStageFlags(t *testing.T) {
	cases := []struct {
		mode            string
		wantFallback    bool
		wantRetired     bool
		wantRetiredBack bool
	}{
		{CutoverModeFallback, true, false, false},
		{CutoverModeNative, false, false, false},
		{CutoverModeRetired, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv(RuntimeCutoverEnvKey, tc.mode)
			if got := RuntimeCutoverOffersLegacyFallback(); got != tc.wantFallback {
				t.Errorf("RuntimeCutoverOffersLegacyFallback() = %v, want %v", got, tc.wantFallback)
			}
			if got := RuntimeCutoverRetired(); got != tc.wantRetired {
				t.Errorf("RuntimeCutoverRetired() = %v, want %v", got, tc.wantRetired)
			}
			if got := IsRetiredChatBackend("opencode"); got != tc.wantRetiredBack {
				t.Errorf("IsRetiredChatBackend(opencode) = %v, want %v", got, tc.wantRetiredBack)
			}
			if IsRetiredChatBackend("msgmate-coder") {
				t.Error("the native runtime must never be treated as a retired chat backend")
			}
			if IsRetiredChatBackend("") {
				t.Error("an empty backend name must not be treated as retired")
			}
		})
	}
}

// TestRuntimeCutoverBotOrder guards the recorded per-bot flip order: the native
// development lead flips last because it orchestrates the other bots, and each
// step before it must still resolve to a native runtime.
func TestRuntimeCutoverBotOrder(t *testing.T) {
	order := RuntimeCutoverBotOrder()
	want := []string{
		"issue-plan-bot",
		"issue-repl-bot",
		"coding-bot",
		"open-chat-go-ci-admin",
		"open-chat-development-lead",
	}
	if len(order) != len(want) {
		t.Fatalf("flip order has %d steps, want %d: %v", len(order), len(want), order)
	}
	for i, legacy := range want {
		if order[i] != legacy {
			t.Errorf("flip step %d = %q, want %q", i, order[i], legacy)
		}
		native, ok := NativeRuntimeBotAlias(legacy)
		if !ok || !IsNativeRuntimeBot(native) {
			t.Errorf("flip step %q does not resolve to a native runtime (got %q, %v)", legacy, native, ok)
		}
	}
	if last := order[len(order)-1]; last != "open-chat-development-lead" {
		t.Errorf("the development lead must flip last, got %q", last)
	}
	// The order is a record: callers must not be able to mutate it.
	order[0] = "mutated"
	if RuntimeCutoverBotOrder()[0] != want[0] {
		t.Fatal("RuntimeCutoverBotOrder() returned the backing slice")
	}
}

// TestRuntimeCutoverStageForBot ensures every bot of the migration reports the
// stage it runs at, and that an unrelated name is not claimed by the cutover.
func TestRuntimeCutoverStageForBot(t *testing.T) {
	t.Setenv(RuntimeCutoverEnvKey, CutoverModeRetired)
	if got := RuntimeCutoverStageForBot(NativeRuntimeReplBot); got != CutoverModeNative {
		t.Errorf("native bot stage = %q, want %q", got, CutoverModeNative)
	}
	if got := RuntimeCutoverStageForBot("issue-repl-bot"); got != CutoverModeRetired {
		t.Errorf("legacy bot stage = %q, want %q", got, CutoverModeRetired)
	}
	if got := RuntimeCutoverStageForBot("an-unrelated-bot"); got != "" {
		t.Errorf("unrelated bot stage = %q, want empty", got)
	}

	t.Setenv(RuntimeCutoverEnvKey, CutoverModeNative)
	if got := RuntimeCutoverStageForBot("issue-repl-bot"); got != CutoverModeNative {
		t.Errorf("legacy bot stage during canary = %q, want %q", got, CutoverModeNative)
	}
}

// TestNormalizeRuntimeSelectorPayloadChoicesFallsBackByDefault ensures a
// selector that still offers a non-native runtime keeps it as a flagged
// fallback while opencode is the immediate rollback (ci#584 default stage).
func TestNormalizeRuntimeSelectorPayloadChoicesFallsBackByDefault(t *testing.T) {
	t.Setenv(RuntimeCutoverEnvKey, CutoverModeFallback)
	payload := map[string]interface{}{
		"type": "runtime-selector",
		"choices": []interface{}{
			map[string]interface{}{"bot": "some-other-runtime", "label": "other"},
			map[string]interface{}{"bot": "issue-plan-bot", "label": "Plan"},
		},
	}
	normalizeRuntimeSelectorPayloadChoices(payload)

	choices, _ := payload["choices"].([]interface{})
	if len(choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(choices))
	}
	first := choices[0].(map[string]interface{})
	if first["bot"] != NativeRuntimePlanBot {
		t.Fatalf("expected the native plan runtime first, got %v", first["bot"])
	}
	second := choices[1].(map[string]interface{})
	if second["fallback"] != true {
		t.Fatalf("expected the non-native runtime to stay a fallback, got %v", second["fallback"])
	}
}

// TestNormalizeRuntimeSelectorPayloadChoicesDropsLegacyWhenRetired ensures the
// retirement stage stops offering non-native runtimes: a stale payload must not
// route a chat back onto a bot that is no longer running (ci#584).
func TestNormalizeRuntimeSelectorPayloadChoicesDropsLegacyWhenRetired(t *testing.T) {
	t.Setenv(RuntimeCutoverEnvKey, CutoverModeRetired)
	payload := map[string]interface{}{
		"type": "runtime-selector",
		"choices": []interface{}{
			map[string]interface{}{"bot": "some-other-runtime", "label": "other"},
			map[string]interface{}{"bot": "issue-plan-bot", "label": "Plan"},
			map[string]interface{}{"bot": "coding-bot", "label": "Code"},
			map[string]interface{}{"bot": "not-a-map"},
		},
	}
	normalizeRuntimeSelectorPayloadChoices(payload)

	choices, _ := payload["choices"].([]interface{})
	if len(choices) != 2 {
		t.Fatalf("expected 2 native choices after retirement, got %d: %v", len(choices), choices)
	}
	for i, raw := range choices {
		choice, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("choice %d is not a map", i)
		}
		bot, _ := choice["bot"].(string)
		if !IsNativeRuntimeBot(bot) {
			t.Fatalf("choice %d still offers non-native bot %q", i, bot)
		}
		if choice["fallback"] == true {
			t.Fatalf("choice %d is flagged as a fallback while retired", i)
		}
	}
	if choices[0].(map[string]interface{})["recommended"] != true {
		t.Fatal("expected the remaining native choice to be recommended")
	}
	if payload["native_runtime_by_default"] != true {
		t.Fatal("expected native_runtime_by_default while retired")
	}
}

// TestNormalizeRuntimeSelectorPayloadChoicesIdempotentWhenRetired ensures the
// second normalization pass (eg a partial and a final delivery of the same
// message) does not resurrect or reorder the retired choices.
func TestNormalizeRuntimeSelectorPayloadChoicesIdempotentWhenRetired(t *testing.T) {
	t.Setenv(RuntimeCutoverEnvKey, CutoverModeRetired)
	payload := map[string]interface{}{
		"type": "runtime-selector",
		"choices": []interface{}{
			map[string]interface{}{"bot": "opencode"},
			map[string]interface{}{"bot": "some-other-runtime"},
			map[string]interface{}{"bot": NativeRuntimePlanBot},
		},
	}
	normalizeRuntimeSelectorPayloadChoices(payload)
	first, _ := payload["choices"].([]interface{})
	if len(first) != 1 {
		t.Fatalf("expected only the native runtime to survive, got %d: %v", len(first), first)
	}
	for _, raw := range first {
		bot, _ := raw.(map[string]interface{})["bot"].(string)
		if !IsNativeRuntimeBot(bot) {
			t.Fatalf("retirement kept a non-native choice %v", bot)
		}
	}

	normalizeRuntimeSelectorPayloadChoices(payload)
	second, _ := payload["choices"].([]interface{})
	if len(second) != len(first) {
		t.Fatalf("choice count changed on second pass: %d != %d", len(second), len(first))
	}
	if second[0].(map[string]interface{})["bot"] != first[0].(map[string]interface{})["bot"] {
		t.Fatal("choice changed on second pass")
	}
}

// TestRetiredRuntimeChoicesKeepOrder guards the filter helper directly.
func TestRetiredRuntimeChoicesKeepOrder(t *testing.T) {
	choices := []interface{}{
		map[string]interface{}{"bot": NativeRuntimePlanBot},
		map[string]interface{}{"bot": "opencode"},
		map[string]interface{}{"bot": NativeRuntimeLeadBot},
	}
	t.Setenv(RuntimeCutoverEnvKey, CutoverModeNative)
	if got := filterRetiredRuntimeChoices(choices); len(got) != len(choices) {
		t.Fatalf("canary stage must keep every choice, got %d", len(got))
	}
	t.Setenv(RuntimeCutoverEnvKey, CutoverModeRetired)
	got := filterRetiredRuntimeChoices(choices)
	if len(got) != 2 {
		t.Fatalf("retired stage must drop opencode, got %d choices", len(got))
	}
	if got[0].(map[string]interface{})["bot"] != NativeRuntimePlanBot ||
		got[1].(map[string]interface{})["bot"] != NativeRuntimeLeadBot {
		t.Fatalf("filter reordered the remaining choices: %v", got)
	}
}
