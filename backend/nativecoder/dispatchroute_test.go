package nativecoder

import (
	"reflect"
	"strings"
	"testing"
)

func devLeadConfig(extra map[string]interface{}) map[string]interface{} {
	config := map[string]interface{}{
		"coder_role": "lead",
		"tools":      []string{"coder_bash", "coder_read"},
	}
	for key, value := range extra {
		config[key] = value
	}
	return config
}

func containsAll(list []string, want []string) bool {
	for _, item := range want {
		found := false
		for _, have := range list {
			if have == item {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestApplyDevLeadDispatchToolsEnablesNativeRoute is the ci#757 exit criterion
// "a lead chat's effective tools include NativeDispatchTools and
// InteractionRecallTools".
func TestApplyDevLeadDispatchToolsEnablesNativeRoute(t *testing.T) {
	config := devLeadConfig(nil)
	out := ApplyDevLeadDispatchTools(config)
	tools, _ := out["tools"].([]string)
	if !containsAll(tools, NativeDispatchTools) {
		t.Fatalf("native dispatch tools missing from %v", tools)
	}
	if !containsAll(tools, InteractionRecallTools) {
		t.Fatalf("interaction recall tools missing from %v", tools)
	}
	if len(tools) != len(NativeDispatchTools)+len(InteractionRecallTools)+2 {
		t.Fatalf("tools duplicated: %v", tools)
	}
}

// TestApplyDevLeadDispatchToolsIsSelective pins the "integrate them selectively
// for development agent chats" requirement: nothing else gains the tools.
func TestApplyDevLeadDispatchToolsIsSelective(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]interface{}
	}{
		{"plain channel config", map[string]interface{}{"tools": []string{"coder_bash"}}},
		{"coder without lead role", map[string]interface{}{"coder_role": "repl", "tools": []string{"coder_bash"}}},
		{"recovery bot", map[string]interface{}{"coder_role": "recovery", "tools": []string{"coder_bash"}}},
		{"no tools configured", map[string]interface{}{"coder_role": "lead"}},
		{"empty tools means unconfigured", map[string]interface{}{"coder_role": "lead", "tools": []string{}}},
		{"opted out", devLeadConfig(map[string]interface{}{HiveMindToolsConfigKey: false})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := make(map[string]interface{}, len(tc.config))
			for key, value := range tc.config {
				before[key] = value
			}
			out := ApplyDevLeadDispatchTools(tc.config)
			if !reflect.DeepEqual(out, before) {
				t.Fatalf("config %s was modified: %v -> %v", tc.name, before, out)
			}
		})
	}
}

// TestApplyDevLeadDispatchToolsKillSwitch covers the deployment-wide rollback.
func TestApplyDevLeadDispatchToolsKillSwitch(t *testing.T) {
	t.Setenv(HiveMindToolsEnvKey, "1")
	out := ApplyDevLeadDispatchTools(devLeadConfig(nil))
	tools, _ := out["tools"].([]string)
	if containsAll(tools, NativeDispatchTools) {
		t.Fatal("kill switch did not roll the enablement back")
	}
	if !HiveMindToolsDisabled("1") || HiveMindToolsDisabled("0") {
		t.Fatal("kill switch parsing is wrong")
	}
}

// TestIsDevLeadConfig covers the declaration forms.
func TestIsDevLeadConfig(t *testing.T) {
	if !IsDevLeadConfig(devLeadConfig(nil)) {
		t.Fatal("coder_role=lead must declare a dev lead")
	}
	if !IsDevLeadConfig(map[string]interface{}{"development_lead": "true"}) {
		t.Fatal("development_lead key must declare a dev lead")
	}
	if IsDevLeadConfig(nil) || IsDevLeadConfig(map[string]interface{}{"coder_role": "repl"}) {
		t.Fatal("non-lead configs must not declare a dev lead")
	}
}

// TestDispatchRoutePlanSnapshotIsStable keeps the ticket tracking intact.
func TestDispatchRoutePlanSnapshotIsStable(t *testing.T) {
	plan := DispatchRoutePlanSnapshot()
	if plan.DeprecatedWorkflow != LegacyDispatchWorkflow || !strings.HasSuffix(LegacyDispatchWorkflow, ".yaml") {
		t.Fatalf("unexpected deprecated workflow %q", plan.DeprecatedWorkflow)
	}
	if plan.NativeTool != NativeDispatchTool || plan.VerifiedHost != "ci.msgmate.io" {
		t.Fatalf("unexpected native route %q / host %q", plan.NativeTool, plan.VerifiedHost)
	}
	if len(plan.Phases) != 4 {
		t.Fatalf("expected 4 phases, got %d", len(plan.Phases))
	}
	seen := map[int]bool{}
	for _, phase := range plan.Phases {
		if phase.ID == "" || len(phase.ExitCriteria) == 0 || len(phase.Tracks) == 0 {
			t.Fatalf("phase %+v is not actionable", phase)
		}
		if seen[phase.Order] {
			t.Fatalf("duplicate phase order %d", phase.Order)
		}
		seen[phase.Order] = true
	}
}

// TestDispatchRouteGateway pins the gate: the workflow may only be retired once
// the native route is verified on ci.msgmate.io.
func TestDispatchRouteGateway(t *testing.T) {
	ready := DispatchRouteEvidence{
		Host:                 "ci.msgmate.io",
		NativeToolsEnabled:   true,
		RoundTripVerified:    true,
		RecallSearchVerified: true,
		StoppedInteractions:  []string{"chat-1"},
	}
	if !DispatchRouteReady(ready) {
		t.Fatalf("expected ready, blockers: %v", DispatchRouteBlockers(ready))
	}
	empty := DispatchRouteEvidence{}
	if DispatchRouteReady(empty) {
		t.Fatal("empty evidence must not pass the gate")
	}
	if len(DispatchRouteBlockers(empty)) != 5 {
		t.Fatalf("expected 5 blockers for empty evidence, got %v", DispatchRouteBlockers(empty))
	}
	wrongHost := ready
	wrongHost.Host = "dev.example.com"
	if DispatchRouteReady(wrongHost) {
		t.Fatal("evidence from another host must not pass the gate")
	}
	legacy := ready
	legacy.LegacyCallsAfter = 1
	if DispatchRouteReady(legacy) {
		t.Fatal("a legacy call after verification must block retirement")
	}
}

// TestClassifyDispatchRouteCall is the evidence classifier of the migration.
func TestClassifyDispatchRouteCall(t *testing.T) {
	cases := []struct {
		sample HiveMindCallSample
		want   string
	}{
		{HiveMindCallSample{Tool: "coder_bash", Arguments: "gh workflow run trigger-open-chat-coding-interaction.yaml"}, "legacy"},
		{HiveMindCallSample{Tool: "coder_bash", Arguments: "ls -la"}, "other"},
		{HiveMindCallSample{Tool: "hive_mind_agent_dispatch"}, "native"},
		{HiveMindCallSample{Tool: "hive_mind_agent_continue"}, "native"},
		{HiveMindCallSample{Tool: "hive_mind_observe_list"}, "observe"},
		{HiveMindCallSample{Tool: "hive_mind_state_history"}, "native"},
		{HiveMindCallSample{Tool: "mcp:open-chat-opencode-mcp:openchat_search_interactions"}, "recall"},
		{HiveMindCallSample{Tool: "coder_edit"}, "other"},
	}
	for _, tc := range cases {
		if got := ClassifyDispatchRouteCall(tc.sample); got != tc.want {
			t.Fatalf("classify(%q) = %q, want %q", tc.sample.Tool, got, tc.want)
		}
	}
}

// TestComputeHiveMindUsage measures past runs: the legacy route dominates, so
// the verdict must say the migration is incomplete.
func TestComputeHiveMindUsage(t *testing.T) {
	report := ComputeHiveMindUsage([]HiveMindCallSample{
		{Tool: "coder_bash", Arguments: "gh workflow run trigger-open-chat-coding-interaction.yaml -f x=1", Chat: "chat-a"},
		{Tool: "coder_bash", Arguments: "gh workflow run trigger-open-chat-coding-interaction.yaml -f x=2", Chat: "chat-a"},
		{Tool: "hive_mind_agent_dispatch", Chat: "chat-b", Failed: true},
		{Tool: "hive_mind_observe_get", Chat: "chat-b"},
		{Tool: "mcp:open-chat-opencode-mcp:openchat_search_interaction_messages", Chat: "chat-b"},
		{Tool: "coder_read", Chat: "chat-b"},
	})
	if report.LegacyCalls != 2 || report.NativeCalls != 1 || report.ObserveCalls != 1 || report.RecallCalls != 1 {
		t.Fatalf("unexpected counters: %+v", report)
	}
	if report.DistinctChats != 2 || report.DistinctNativeChats != 1 {
		t.Fatalf("unexpected chat counts: %+v", report)
	}
	if got := strings.Join(report.LegacyChats, ","); got != "chat-a" {
		t.Fatalf("legacy chats: %q", got)
	}
	if got := strings.Join(report.NativeChats, ","); got != "chat-b" {
		t.Fatalf("native chats: %q", got)
	}
	if report.LegacyShare <= report.NativeShare || report.NativeShare <= 0 {
		t.Fatalf("unexpected shares: legacy %.2f native %.2f", report.LegacyShare, report.NativeShare)
	}
	if !strings.Contains(report.Verdict, "both routes are in use") {
		t.Fatalf("unexpected mixed-route verdict: %q", report.Verdict)
	}
	failed := ComputeHiveMindUsage([]HiveMindCallSample{
		{Tool: "hive_mind_agent_dispatch", Chat: "chat-b", Failed: true},
		{Tool: "hive_mind_observe_list", Chat: "chat-b"},
		{Tool: "mcp:open-chat-opencode-mcp:openchat_search_interactions", Chat: "chat-b"},
	})
	if !strings.Contains(failed.Verdict, "failing") {
		t.Fatalf("failing native calls must be reported: %q", failed.Verdict)
	}

	idle := ComputeHiveMindUsage(nil)
	if idle.LegacyCalls != 0 || idle.NativeCalls != 0 || idle.LegacyShare != 0 || idle.NativeShare != 0 {
		t.Fatalf("unexpected idle report: %+v", idle)
	}
	if !strings.Contains(idle.Verdict, "no trigger calls") {
		t.Fatalf("unexpected idle verdict: %q", idle.Verdict)
	}

	healthy := ComputeHiveMindUsage([]HiveMindCallSample{
		{Tool: "hive_mind_agent_dispatch", Chat: "chat-c"},
		{Tool: "hive_mind_observe_list", Chat: "chat-c"},
		{Tool: "mcp:open-chat-opencode-mcp:openchat_list_interactions", Chat: "chat-c"},
	})
	if !strings.Contains(healthy.Verdict, "may be retired") {
		t.Fatalf("healthy verdict must allow retirement: %q", healthy.Verdict)
	}
	if healthy.NativeShare != 1 {
		t.Fatalf("expected full native share, got %.2f", healthy.NativeShare)
	}
}

// TestRenderDispatchRouteMarkdown keeps the run evidence renderable and free of
// anything that could leak a credential.
func TestRenderDispatchRouteMarkdown(t *testing.T) {
	marked := RenderDispatchRouteMarkdown(DispatchRouteEvidence{
		Host:                 "ci.msgmate.io",
		NativeToolsEnabled:   true,
		RoundTripVerified:    true,
		RecallSearchVerified: true,
		StoppedInteractions:  []string{"chat-1"},
	}, nil)
	if !strings.Contains(marked, "gate: ready") || strings.Contains(marked, "gate: blocked") {
		t.Fatalf("unexpected gate rendering: %s", marked)
	}
	for _, forbidden := range []string{"token", "kubeconfig", "password"} {
		if strings.Contains(strings.ToLower(marked), forbidden) {
			t.Fatalf("rendered plan mentions %q", forbidden)
		}
	}
	blocked := RenderDispatchRouteMarkdown(DispatchRouteEvidence{}, nil)
	if !strings.Contains(blocked, "gate: blocked") {
		t.Fatalf("unexpected blocked rendering: %s", blocked)
	}

	report := &HiveMindUsageReport{Version: 1, NativeCalls: 2, LegacyCalls: 0, NativeShare: 1, Verdict: "ok"}
	jsonOut := DispatchRouteJSON(DispatchRouteEvidence{Host: "ci.msgmate.io"}, report)
	if !strings.Contains(jsonOut, `"ready": false`) || !strings.Contains(jsonOut, `"usage"`) {
		t.Fatalf("unexpected json evidence: %s", jsonOut)
	}
	if strings.Contains(jsonOut, "secrets") {
		t.Fatalf("json evidence must stay free of credentials: %s", jsonOut)
	}
}
