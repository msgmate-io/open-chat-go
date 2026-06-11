package nativecoder

import (
	"strings"
	"testing"
)

func devAgentTestConfig(extra map[string]interface{}) map[string]interface{} {
	config := map[string]interface{}{
		"coder_mode": "native",
		"model":      "gpt-4o",
		"git_repo":   "msgmate-io/open-chat-go",
		"autonomous": true,
	}
	for key, value := range extra {
		config[key] = value
	}
	return config
}

func TestApplyDevAgentHiveMindConfigWidensCoderTools(t *testing.T) {
	config, merged := ApplyDevAgentHiveMindConfig(devAgentTestConfig(nil), "")
	if !merged {
		t.Fatal("expected the hive-mind family to be merged into a coder config")
	}
	present := map[string]bool{}
	for _, name := range stringSliceFromConfig(config["tools"]) {
		present[name] = true
	}
	for _, name := range DevAgentHiveMindToolNames() {
		if !present[name] {
			t.Fatalf("merged tools missing %q", name)
		}
	}
	readiness := EvaluateDevAgentDispatchReadiness(config)
	if !readiness.DispatchReady() {
		t.Fatalf("expected a dispatch-ready config, got %+v", readiness)
	}
	if readiness.LegacyRouteRequired() {
		t.Fatal("legacy workflow route should no longer be required")
	}
	if len(readiness.MissingRequiredTools) != 0 || len(readiness.MissingRecallTools) != 0 {
		t.Fatalf("unexpected missing tools: %+v", readiness)
	}
}

func TestApplyDevAgentHiveMindConfigIsIdempotent(t *testing.T) {
	config, merged := ApplyDevAgentHiveMindConfig(devAgentTestConfig(nil), "")
	if !merged {
		t.Fatal("expected merge on the first pass")
	}
	first := len(stringSliceFromConfig(config["tools"]))
	_, mergedAgain := ApplyDevAgentHiveMindConfig(config, "")
	if mergedAgain {
		t.Fatal("second pass should be a no-op")
	}
	if len(stringSliceFromConfig(config["tools"])) != first {
		t.Fatal("idempotent merge changed the tool list length")
	}
}

func TestApplyDevAgentHiveMindConfigRespectsExplicitTools(t *testing.T) {
	config := devAgentTestConfig(map[string]interface{}{"tools": []interface{}{"coder_bash"}})
	if !HasExplicitTools(config) {
		t.Fatal("HasExplicitTools should report an explicit list")
	}
	_, merged := ApplyDevAgentHiveMindConfig(config, "")
	if merged {
		t.Fatal("an explicit non-empty tools list must win")
	}
	tools := stringSliceFromConfig(config["tools"])
	if len(tools) != 1 || tools[0] != "coder_bash" {
		t.Fatalf("explicit tools were modified: %v", tools)
	}
	// An operator-narrowed list means the deprecated workflow route is still
	// the only dispatch path for that agent.
	if !EvaluateDevAgentDispatchReadiness(config).LegacyRouteRequired() {
		t.Fatal("expected the legacy route to still be required")
	}
}

func TestApplyDevAgentHiveMindConfigIgnoresNonCoderConfigs(t *testing.T) {
	config := map[string]interface{}{"model": "gpt-4o"}
	_, merged := ApplyDevAgentHiveMindConfig(config, "")
	if merged {
		t.Fatal("non-coder configs must not receive the dev-agent family")
	}
	if _, ok := config["tools"]; ok {
		t.Fatal("non-coder config was modified")
	}
}

func TestApplyDevAgentHiveMindConfigKillSwitches(t *testing.T) {
	// Deployment-wide kill switch.
	config := devAgentTestConfig(nil)
	if _, merged := ApplyDevAgentHiveMindConfig(config, "off"); merged {
		t.Fatal("deployment kill switch must disable the merge")
	}
	// Per-agent opt-out.
	config = devAgentTestConfig(map[string]interface{}{DevAgentHiveMindConfigKey: false})
	if _, merged := ApplyDevAgentHiveMindConfig(config, ""); merged {
		t.Fatal("per-agent opt-out must disable the merge")
	}
	// Per-agent opt-out as a false-like string, and a truthy value that keeps
	// the default on.
	config = devAgentTestConfig(map[string]interface{}{DevAgentHiveMindConfigKey: " disabled "})
	if _, merged := ApplyDevAgentHiveMindConfig(config, ""); merged {
		t.Fatal("false-like string opt-out must disable the merge")
	}
	config = devAgentTestConfig(map[string]interface{}{DevAgentHiveMindConfigKey: "yes"})
	if _, merged := ApplyDevAgentHiveMindConfig(config, ""); !merged {
		t.Fatal("truthy override must keep the default-on merge")
	}
}

func TestDevAgentHiveMindConfigEnabledDefaults(t *testing.T) {
	if !DevAgentHiveMindConfigEnabled(nil) {
		t.Fatal("nil config should keep the family enabled")
	}
	if !DevAgentHiveMindConfigEnabled(map[string]interface{}{}) {
		t.Fatal("missing key should keep the family enabled")
	}
	if DevAgentHiveMindConfigEnabled(map[string]interface{}{DevAgentHiveMindConfigKey: false}) {
		t.Fatal("explicit false should disable the family")
	}
	if !DevAgentHiveMindConfigEnabled(map[string]interface{}{DevAgentHiveMindConfigKey: 0}) {
		t.Fatal("non-bool/non-string values should keep the family enabled")
	}
}

func TestHasExplicitTools(t *testing.T) {
	if HasExplicitTools(nil) {
		t.Fatal("nil config has no explicit tools")
	}
	if HasExplicitTools(map[string]interface{}{}) {
		t.Fatal("missing key has no explicit tools")
	}
	if HasExplicitTools(map[string]interface{}{"tools": []interface{}{}}) {
		t.Fatal("empty list is not an explicit choice")
	}
	if !HasExplicitTools(map[string]interface{}{"tools": []string{"coder_bash"}}) {
		t.Fatal("non-empty list is an explicit choice")
	}
}

func TestEvaluateDevAgentDispatchReadinessNilConfig(t *testing.T) {
	readiness := EvaluateDevAgentDispatchReadiness(nil)
	if readiness.DispatchReady() {
		t.Fatal("a nil config cannot be dispatch ready")
	}
	if !readiness.LegacyRouteRequired() {
		t.Fatal("a nil config still needs the legacy route")
	}
	if len(readiness.MissingRequiredTools) != len(DevAgentHiveMindRequiredTools()) {
		t.Fatalf("expected all required tools missing, got %v", readiness.MissingRequiredTools)
	}
}

func TestDevAgentHiveMindDeprecationNoticeMentionsNativeDispatch(t *testing.T) {
	if DevAgentHiveMindDeprecationNotice == "" {
		t.Fatal("deprecation notice must not be empty")
	}
	// The notice is the shared wording for the retired workflow route; make sure
	// it names both the deprecated workflow and the native replacement.
	for _, want := range []string{"trigger-coding-interaction", "hive_mind_agent_", "ci#757"} {
		if !strings.Contains(DevAgentHiveMindDeprecationNotice, want) {
			t.Fatalf("notice %q is missing %q", DevAgentHiveMindDeprecationNotice, want)
		}
	}
}
