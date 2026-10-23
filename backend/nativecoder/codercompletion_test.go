package nativecoder

import (
	"strings"
	"testing"
)

func coderCompletionTestConfig(extra map[string]interface{}) map[string]interface{} {
	config := map[string]interface{}{
		"coder_mode": "native",
		"model":      "gpt-4o",
		"git_repo":   "msgmate-io/open-chat-go",
	}
	for key, value := range extra {
		config[key] = value
	}
	return config
}

func coderCompletionTools(config map[string]interface{}) []string {
	return stringSliceFromConfig(config["tools"])
}

func TestApplyCoderTaskCompletionConfigMergesTool(t *testing.T) {
	config, merged := ApplyCoderTaskCompletionConfig(coderCompletionTestConfig(nil), "")
	if !merged {
		t.Fatal("expected the completion tool to be merged into a coder config")
	}
	if !HasCoderTaskCompletionTool(coderCompletionTools(config)) {
		t.Fatalf("merged tools missing %q: %v", AllTasksCompletedToolName, coderCompletionTools(config))
	}
	if !HasAllCoderTaskCompletionTools(coderCompletionTools(config)) {
		t.Fatal("HasAllCoderTaskCompletionTools should report the merged surface")
	}
}

func TestApplyCoderTaskCompletionConfigIsIdempotent(t *testing.T) {
	config, _ := ApplyCoderTaskCompletionConfig(coderCompletionTestConfig(nil), "")
	first := coderCompletionTools(config)
	if _, mergedAgain := ApplyCoderTaskCompletionConfig(config, ""); mergedAgain {
		t.Fatal("second pass should be a no-op")
	}
	if len(coderCompletionTools(config)) != len(first) {
		t.Fatal("idempotent merge changed the tool list length")
	}
}

func TestApplyCoderTaskCompletionConfigRespectsExplicitTools(t *testing.T) {
	config := coderCompletionTestConfig(map[string]interface{}{
		"tools": []interface{}{"coder_bash"},
	})
	if _, merged := ApplyCoderTaskCompletionConfig(config, ""); merged {
		t.Fatal("an explicit non-empty tools list must win")
	}
	tools := coderCompletionTools(config)
	if len(tools) != 1 || tools[0] != "coder_bash" {
		t.Fatalf("explicit tools were modified: %v", tools)
	}
	if HasCoderTaskCompletionTool(tools) {
		t.Fatal("the prompt layer must not advertise an operator-narrowed surface")
	}
}

func TestApplyCoderTaskCompletionConfigIgnoresNonCoderConfigs(t *testing.T) {
	config := map[string]interface{}{"model": "gpt-4o"}
	if _, merged := ApplyCoderTaskCompletionConfig(config, ""); merged {
		t.Fatal("non-coder configs must not receive the completion tool")
	}
	if _, ok := config["tools"]; ok {
		t.Fatal("non-coder config was modified")
	}
}

func TestApplyCoderTaskCompletionConfigNilConfig(t *testing.T) {
	config, merged := ApplyCoderTaskCompletionConfig(nil, "")
	if merged || config != nil {
		t.Fatal("nil config should be a safe no-op")
	}
}

func TestApplyCoderTaskCompletionConfigKillSwitches(t *testing.T) {
	// Deployment-wide kill switch.
	if _, merged := ApplyCoderTaskCompletionConfig(coderCompletionTestConfig(nil), "off"); merged {
		t.Fatal("deployment kill switch must disable the merge")
	}
	if !CoderTaskCompletionEnvDisabled(" disabled ") {
		t.Fatal("false-like env values should be recognised")
	}
	if CoderTaskCompletionEnvDisabled("") || CoderTaskCompletionEnvDisabled("yes") {
		t.Fatal("empty/truthy env values must keep the default on")
	}
	// Per-agent opt-out.
	config := coderCompletionTestConfig(map[string]interface{}{CoderTaskCompletionConfigKey: false})
	if _, merged := ApplyCoderTaskCompletionConfig(config, ""); merged {
		t.Fatal("per-agent opt-out must disable the merge")
	}
	// False-like string opt-out and a truthy override.
	config = coderCompletionTestConfig(map[string]interface{}{CoderTaskCompletionConfigKey: " disabled "})
	if _, merged := ApplyCoderTaskCompletionConfig(config, ""); merged {
		t.Fatal("false-like string opt-out must disable the merge")
	}
	config = coderCompletionTestConfig(map[string]interface{}{CoderTaskCompletionConfigKey: "yes"})
	if _, merged := ApplyCoderTaskCompletionConfig(config, ""); !merged {
		t.Fatal("truthy override must keep the default-on merge")
	}
}

func TestCoderTaskCompletionConfigEnabledDefaults(t *testing.T) {
	if !CoderTaskCompletionConfigEnabled(nil) {
		t.Fatal("nil config should keep the tool enabled")
	}
	if !CoderTaskCompletionConfigEnabled(map[string]interface{}{}) {
		t.Fatal("missing key should keep the tool enabled")
	}
	if CoderTaskCompletionConfigEnabled(map[string]interface{}{CoderTaskCompletionConfigKey: false}) {
		t.Fatal("explicit false should disable the tool")
	}
	if !CoderTaskCompletionConfigEnabled(map[string]interface{}{CoderTaskCompletionConfigKey: 0}) {
		t.Fatal("non-bool/non-string values should keep the tool enabled")
	}
}

func TestHasCoderTaskCompletionTool(t *testing.T) {
	if HasCoderTaskCompletionTool(nil) {
		t.Fatal("nil tools must not report the completion tool")
	}
	if HasCoderTaskCompletionTool(CoderSurfaceTools()) {
		t.Fatal("the bare coder surface must not report the completion tool")
	}
	if !HasCoderTaskCompletionTool([]string{"coder_bash", AllTasksCompletedToolName}) {
		t.Fatal("a widened surface should report the completion tool")
	}
}

func TestCoderTaskCompletionGuidance(t *testing.T) {
	guidance := CoderTaskCompletionGuidance()
	if !strings.Contains(guidance, AllTasksCompletedToolName) {
		t.Fatalf("guidance should name the tool: %q", guidance)
	}
	if !strings.Contains(guidance, "stop") {
		t.Fatalf("guidance should tell the model to stop: %q", guidance)
	}
}
