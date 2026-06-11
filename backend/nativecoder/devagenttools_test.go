package nativecoder

import "testing"

func TestDevAgentHiveMindToolNames(t *testing.T) {
	names := DevAgentHiveMindToolNames()
	if len(names) == 0 {
		t.Fatal("expected a non-empty hive-mind tool family")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" {
			t.Fatal("empty tool name in family")
		}
		if seen[name] {
			t.Fatalf("duplicate tool name %q", name)
		}
		seen[name] = true
	}
	for _, want := range []string{"hive_mind_agent_dispatch", "hive_mind_observe_list"} {
		if !seen[want] {
			t.Fatalf("family is missing %q", want)
		}
	}
	// The dispatch family must be a subset of the full family.
	for _, required := range DevAgentHiveMindRequiredTools() {
		if !seen[required] {
			t.Fatalf("required tool %q missing from the family", required)
		}
	}
	for _, recall := range DevAgentHiveMindRecallToolNames() {
		if !seen[recall] {
			t.Fatalf("recall tool %q missing from the family", recall)
		}
	}
	if len(DevAgentHiveMindRequiredTools()) == 0 || len(DevAgentHiveMindRecallToolNames()) == 0 {
		t.Fatal("expected both the dispatch and the recall families to be non-empty")
	}
	// The init keys must be part of the family so tool_init cannot request an
	// unknown name.
	for _, initKey := range DevAgentHiveMindToolInitKeys() {
		if !seen[initKey] {
			t.Fatalf("tool_init key %q missing from the family", initKey)
		}
	}
}

func TestIsDevAgentRole(t *testing.T) {
	positive := []string{"lead", "Lead", "dev-lead", "Development Lead", "dev_agent", "coder", "coder-agent-2", "native-coder"}
	for _, role := range positive {
		if !IsDevAgentRole(role) {
			t.Fatalf("IsDevAgentRole(%q) = false, want true", role)
		}
	}
	negative := []string{"", "   ", "user", "msgmate", "assistant", "leadership"}
	for _, role := range negative {
		if IsDevAgentRole(role) {
			t.Fatalf("IsDevAgentRole(%q) = true, want false", role)
		}
	}
}

func TestApplyDevAgentHiveMindToolsInjectsByDefault(t *testing.T) {
	tools, injected := ApplyDevAgentHiveMindTools("lead", []string{"coder_bash"}, false, "")
	if !injected {
		t.Fatal("expected the hive-mind family to be injected for a dev-agent chat")
	}
	got := map[string]bool{}
	for _, name := range tools {
		got[name] = true
	}
	if !got["coder_bash"] {
		t.Fatal("existing tool coder_bash was dropped")
	}
	for _, want := range DevAgentHiveMindToolNames() {
		if !got[want] {
			t.Fatalf("injected list is missing %q", want)
		}
	}
}

func TestApplyDevAgentHiveMindToolsIsIdempotent(t *testing.T) {
	first, injected := ApplyDevAgentHiveMindTools("dev-agent", nil, false, "")
	if !injected {
		t.Fatal("expected injection on the first pass")
	}
	second, injectedAgain := ApplyDevAgentHiveMindTools("dev-agent", first, false, "")
	if injectedAgain {
		t.Fatal("second pass should be a no-op")
	}
	if len(second) != len(first) {
		t.Fatalf("idempotent merge changed the list: %d -> %d entries", len(first), len(second))
	}
}

func TestApplyDevAgentHiveMindToolsRespectsExplicitConfig(t *testing.T) {
	configured := []string{"coder_bash"}
	tools, injected := ApplyDevAgentHiveMindTools("lead", configured, true, "")
	if injected {
		t.Fatal("an explicit non-empty tools list must win over auto-injection")
	}
	if len(tools) != 1 || tools[0] != "coder_bash" {
		t.Fatalf("explicit tools list was modified: %v", tools)
	}
}

func TestApplyDevAgentHiveMindToolsSkipsNonDevAgentChats(t *testing.T) {
	tools, injected := ApplyDevAgentHiveMindTools("user", []string{"coder_bash"}, false, "")
	if injected {
		t.Fatal("end-user chats must not receive the dev-agent hive-mind family")
	}
	if len(tools) != 1 {
		t.Fatalf("tools list changed for a non-dev-agent chat: %v", tools)
	}
}

func TestApplyDevAgentHiveMindToolsKillSwitch(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off", "disabled", " OFF "} {
		if !DevAgentHiveMindEnvDisabled(value) {
			t.Fatalf("DevAgentHiveMindEnvDisabled(%q) = false, want true", value)
		}
		_, injected := ApplyDevAgentHiveMindTools("lead", nil, false, value)
		if injected {
			t.Fatalf("no injection expected for kill switch %q", value)
		}
	}
	for _, value := range []string{"", "1", "true", "on", "yes"} {
		if DevAgentHiveMindEnvDisabled(value) {
			t.Fatalf("DevAgentHiveMindEnvDisabled(%q) = true, want false", value)
		}
	}
}

func TestMergeToolNamesDeduplicatesAndSorts(t *testing.T) {
	got := mergeToolNames([]string{"b", "a", "b", " "}, []string{"c", "a"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// The inputs must not be mutated.
	base := []string{"b", "a"}
	mergeToolNames(base, []string{"c"})
	if len(base) != 2 || base[0] != "b" || base[1] != "a" {
		t.Fatalf("mergeToolNames mutated its input: %v", base)
	}
}

func TestEvaluateHiveMindUsage(t *testing.T) {
	report := EvaluateHiveMindUsage([]HiveMindUsageSample{
		{ChatUUID: "a", Bot: "coder", Tools: []string{"hive_mind_agent_dispatch", "hive_mind_agent_list"}},
		{ChatUUID: "b", Bot: "coder", Tools: []string{"coder_bash"}},
		{ChatUUID: "c", Bot: "lead", Tools: []string{"hive_mind_agent_dispatch"}},
	})
	if report.Chats != 3 {
		t.Fatalf("Chats = %d, want 3", report.Chats)
	}
	if report.ChatsUsingHiveMind != 2 {
		t.Fatalf("ChatsUsingHiveMind = %d, want 2", report.ChatsUsingHiveMind)
	}
	if report.ToolCalls["hive_mind_agent_dispatch"] != 2 {
		t.Fatalf("dispatch calls = %d, want 2", report.ToolCalls["hive_mind_agent_dispatch"])
	}
	if report.Coverage < 0.66 || report.Coverage > 0.67 {
		t.Fatalf("Coverage = %v, want ~0.667", report.Coverage)
	}
	unused := map[string]bool{}
	for _, name := range report.UnusedHiveMindTools {
		unused[name] = true
	}
	if unused["hive_mind_agent_dispatch"] || unused["hive_mind_agent_list"] {
		t.Fatalf("used tools reported as unused: %v", report.UnusedHiveMindTools)
	}
	if !unused["hive_mind_agent_stop"] {
		t.Fatalf("unused tool hive_mind_agent_stop missing from %v", report.UnusedHiveMindTools)
	}
}

func TestEvaluateHiveMindUsageEmpty(t *testing.T) {
	report := EvaluateHiveMindUsage(nil)
	if report.Chats != 0 || report.Coverage != 0 {
		t.Fatalf("empty input should yield a zero report, got %+v", report)
	}
	if len(report.UnusedHiveMindTools) != len(DevAgentHiveMindToolNames()) {
		t.Fatalf("expected all tools unused, got %v", report.UnusedHiveMindTools)
	}
}
