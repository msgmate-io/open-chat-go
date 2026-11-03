package nativecoder

import (
	"testing"
)

// TestAutoConsolidationDefaultIsOnForCoderAgents is the default-ON test of
// ci#653: a config that declares a native coder agent (here by its role key)
// gets the standard `auto_consolidation` block merged in, so consolidation is a
// first-class default rather than an opt-in.
func TestAutoConsolidationDefaultIsOnForCoderAgents(t *testing.T) {
	config := map[string]interface{}{
		"model":             "some-model",
		"backend":           "litellm",
		"coder_interaction": "autonomous",
	}

	applied := ApplyAutoConsolidationDefault(config, "")

	raw, ok := applied[AutoConsolidationConfigKey].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an auto_consolidation block, got %#v", applied[AutoConsolidationConfigKey])
	}
	if raw["enabled"] != true {
		t.Fatalf("expected enabled=true by default, got %#v", raw["enabled"])
	}
	if raw["threshold"] != DefaultAutoConsolidationThreshold {
		t.Fatalf("threshold = %#v, want %d", raw["threshold"], DefaultAutoConsolidationThreshold)
	}
	if raw["base"] != DefaultAutoConsolidationBase {
		t.Fatalf("base = %#v, want %q", raw["base"], DefaultAutoConsolidationBase)
	}
	if raw["scope"] != DefaultAutoConsolidationScope {
		t.Fatalf("scope = %#v, want %q", raw["scope"], DefaultAutoConsolidationScope)
	}
	if raw["branch_prefix"] != DefaultAutoConsolidationBranchPrefix {
		t.Fatalf("branch_prefix = %#v, want %q", raw["branch_prefix"], DefaultAutoConsolidationBranchPrefix)
	}
	if raw["require_green"] != true || raw["one_open_at_a_time"] != true || raw["rollup_issue"] != true {
		t.Fatalf("expected require_green/one_open_at_a_time/rollup_issue true, got %#v", raw)
	}
	authors, ok := raw["authors"].([]interface{})
	if !ok || len(authors) != 1 || authors[0] != AuthorSelf {
		t.Fatalf("expected authors [self], got %#v", raw["authors"])
	}
	labels, ok := raw["labels"].([]interface{})
	if !ok || len(labels) != len(DefaultAutoConsolidationLabels) {
		t.Fatalf("expected the standard label set, got %#v", raw["labels"])
	}
}

// TestAutoConsolidationDefaultRecognisesCoderByToolOrToolInit guards that every
// native coder marker (a coder_* tool, the coder_select_workspace tool_init
// entry, or a sandbox/provider key) declares the agent.
func TestAutoConsolidationDefaultRecognisesCoderByToolOrToolInit(t *testing.T) {
	configs := []map[string]interface{}{
		{"tools": []interface{}{"coder_read", "coder_bash"}},
		{"tool_init": map[string]interface{}{"coder_select_workspace": map[string]interface{}{"mode": "build"}}},
		{"coder_sandbox_backend": "kubernetes"},
		{"coder_provider_max_concurrency": 3},
	}
	for idx, config := range configs {
		applied := ApplyAutoConsolidationDefault(config, "")
		if _, ok := applied[AutoConsolidationConfigKey]; !ok {
			t.Fatalf("config %d: expected the default to apply, got %#v", idx, applied)
		}
	}
}

// TestAutoConsolidationDefaultSkipsNonCoderAgents guards that a plain assistant
// or an opencode bot is never touched.
func TestAutoConsolidationDefaultSkipsNonCoderAgents(t *testing.T) {
	config := map[string]interface{}{
		"model":   "some-model",
		"backend": "opencode",
		"tools":   []interface{}{"read", "list"},
	}
	applied := ApplyAutoConsolidationDefault(config, "")
	if _, ok := applied[AutoConsolidationConfigKey]; ok {
		t.Fatalf("a non-coder config must not get auto_consolidation: %#v", applied)
	}
}

// TestAutoConsolidationDefaultRespectsExplicitToggle is the toggle/rollback test
// of ci#653: an explicit per-agent config always wins, so a single agent can be
// switched off (`enabled: false`) or fully overridden.
func TestAutoConsolidationDefaultRespectsExplicitToggle(t *testing.T) {
	off := map[string]interface{}{
		"coder_interaction": "autonomous",
		AutoConsolidationConfigKey: map[string]interface{}{
			"enabled": false,
		},
	}
	applied := ApplyAutoConsolidationDefault(off, "")
	raw, ok := applied[AutoConsolidationConfigKey].(map[string]interface{})
	if !ok || raw["enabled"] != false {
		t.Fatalf("explicit enabled=false must be preserved, got %#v", applied[AutoConsolidationConfigKey])
	}

	// Explicit boolean shorthand is preserved too.
	boolOff := map[string]interface{}{
		"coder_interaction":        "autonomous",
		AutoConsolidationConfigKey: false,
	}
	appliedBool := ApplyAutoConsolidationDefault(boolOff, "")
	if appliedBool[AutoConsolidationConfigKey] != false {
		t.Fatalf("explicit boolean false must be preserved, got %#v", appliedBool[AutoConsolidationConfigKey])
	}
}

// TestAutoConsolidationKillSwitchRollsTheDefaultBack is the fleet-wide rollback
// test: a false-like deployment value disables the default for every agent, with
// no per-agent config needed.
func TestAutoConsolidationKillSwitchRollsTheDefaultBack(t *testing.T) {
	config := map[string]interface{}{"coder_interaction": "autonomous"}

	applied := ApplyAutoConsolidationDefault(config, "off")
	if _, ok := applied[AutoConsolidationConfigKey]; ok {
		t.Fatalf("kill switch 'off' must suppress the default, got %#v", applied)
	}
	if !AutoConsolidationEnvDisabled("0") || !AutoConsolidationEnvDisabled("false") || !AutoConsolidationEnvDisabled("no") || !AutoConsolidationEnvDisabled("disabled") {
		t.Fatalf("expected 0/false/no/disabled to disable the default")
	}
	if AutoConsolidationEnvDisabled("") || AutoConsolidationEnvDisabled("1") || AutoConsolidationEnvDisabled("true") {
		t.Fatalf("expected empty/1/true to keep the default on")
	}
}

// TestDefaultAutoConsolidationTargetsDevTrunk pins the ci#833/ci#931 integration
// trunk: a consolidation PR defaults to `dev` with only the lightweight
// verification labels. The heavy post-deploy suites belong to the automatic
// `dev` -> `main` promotion PR, never to a consolidation PR (a regression here
// makes every feature PR burn the sandbox/AI suites again).
func TestDefaultAutoConsolidationTargetsDevTrunk(t *testing.T) {
	if DefaultAutoConsolidationBase != "dev" {
		t.Fatalf("DefaultAutoConsolidationBase = %q, want \"dev\" (the integration trunk)", DefaultAutoConsolidationBase)
	}
	allowed := map[string]bool{"test": true, "e2e": true}
	if len(DefaultAutoConsolidationLabels) != len(allowed) {
		t.Fatalf("DefaultAutoConsolidationLabels = %v, want exactly %v", DefaultAutoConsolidationLabels, []string{"test", "e2e"})
	}
	heavy := map[string]bool{
		"deploy": true, "sandbox-test": true, "build": true,
		"test-core-only": true, "ai-test-mock": true, "ai-test-real": true,
	}
	for _, label := range DefaultAutoConsolidationLabels {
		if !allowed[label] {
			t.Fatalf("DefaultAutoConsolidationLabels contains %q; only test/e2e belong on a dev-targeted consolidation PR", label)
		}
		if heavy[label] {
			t.Fatalf("DefaultAutoConsolidationLabels contains the heavy label %q; it belongs on the dev -> main promotion PR", label)
		}
	}
}

// TestResolvePolicyFromConfigDefaultsOn guards resolution: absent key -> default
// ON (unless the deployment kill switch is set), map form -> overridden fields.
func TestResolvePolicyFromConfigDefaultsOn(t *testing.T) {
	t.Setenv(AutoConsolidationEnvKey, "")

	policy := ResolvePolicyFromConfig(map[string]interface{}{"coder_interaction": "autonomous"})
	if !policy.Enabled {
		t.Fatalf("expected the default policy to be enabled")
	}
	if policy.Threshold != 1 || policy.Base != "dev" || !policy.RequireGreen || !policy.RequireIssueLink || !policy.OneOpenAtATime {
		t.Fatalf("unexpected default policy: %#v", policy)
	}

	override := ResolveAutoConsolidationPolicy(map[string]interface{}{
		"enabled":            true,
		"threshold":          float64(3),
		"base":               "develop",
		"authors":            []interface{}{"*"},
		"labels":             "test,build",
		"one_open_at_a_time": false,
	})
	if override.Threshold != 3 {
		t.Fatalf("threshold = %d, want 3", override.Threshold)
	}
	if override.Base != "develop" {
		t.Fatalf("base = %q, want develop", override.Base)
	}
	if override.OneOpenAtATime {
		t.Fatalf("one_open_at_a_time should be false")
	}
	if len(override.Labels) != 2 || override.Labels[0] != "test" || override.Labels[1] != "build" {
		t.Fatalf("labels = %#v, want [test build]", override.Labels)
	}
	if len(override.Authors) != 1 || override.Authors[0] != AuthorAny {
		t.Fatalf("authors = %#v, want [*]", override.Authors)
	}
}

// TestAutoConsolidationEnabledForConfig covers the single call the completion
// choke point uses.
func TestAutoConsolidationEnabledForConfig(t *testing.T) {
	t.Setenv(AutoConsolidationEnvKey, "")

	coder := map[string]interface{}{"coder_interaction": "autonomous"}
	if !AutoConsolidationEnabledForConfig(coder) {
		t.Fatalf("expected auto consolidation to be on for a coder agent by default")
	}

	if AutoConsolidationEnabledForConfig(map[string]interface{}{"model": "m", "backend": "b"}) {
		t.Fatalf("expected auto consolidation to be off for a non-coder config")
	}

	disabled := map[string]interface{}{
		"coder_interaction":        "autonomous",
		AutoConsolidationConfigKey: map[string]interface{}{"enabled": false},
	}
	if AutoConsolidationEnabledForConfig(disabled) {
		t.Fatalf("expected an explicit enabled=false to win")
	}
}
