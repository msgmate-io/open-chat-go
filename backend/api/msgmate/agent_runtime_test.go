package msgmate

import (
	"context"
	"testing"
)

// ci#394 (Host v5 Agent runtime): the integration-owned turn spec and its hooks
// must reach the shared tool loop without changing the default behaviour when no
// spec is installed.

func TestRunAgentTurnWithoutTurnContextFails(t *testing.T) {
	if _, err := RunAgentTurn(context.Background(), AgentTurnSpec{}); err == nil {
		t.Fatal("RunAgentTurn without a bot-reply turn context: want error, got nil")
	}
}

func TestAgentHooksRoundTrip(t *testing.T) {
	normalized := false
	spec := AgentTurnSpec{
		SystemPrompt: "system",
		Tools:        []string{"coder_bash"},
		Hooks: AgentTurnHooks{
			NormalizeToolCall: func(name, arguments string) string {
				normalized = true
				return `{"command":"ls"}`
			},
			ToolBudgetExempt: func(name string) bool { return name == "coder_schedule" },
		},
	}
	ctx := withAgentTurnSpec(context.Background(), spec)

	got, ok := agentNormalizeToolCall(ctx, "coder_bash", `{"command":"cd /workspace && ls"}`)
	if !ok || got != `{"command":"ls"}` || !normalized {
		t.Fatalf("agentNormalizeToolCall = %q, ok=%v (normalized=%v)", got, ok, normalized)
	}
	// A tool with no hook installed reports ok=false so the caller keeps its
	// own fallback.
	if _, ok := agentNormalizeToolCall(context.Background(), "coder_bash", "{}"); ok {
		t.Fatal("agentNormalizeToolCall without a hook: want ok=false")
	}
	if !agentToolBudgetExempt(ctx, "coder_schedule") {
		t.Fatal("agentToolBudgetExempt(coder_schedule): want true")
	}
	if agentToolBudgetExempt(ctx, "coder_read") {
		t.Fatal("agentToolBudgetExempt(coder_read): want false")
	}
}

func TestMergeAgentToolInitPrecedence(t *testing.T) {
	ctx := withAgentTurnSpec(context.Background(), AgentTurnSpec{
		Hooks: AgentTurnHooks{
			PreCallInit: func(AgentInitContext) map[string]map[string]interface{} {
				return map[string]map[string]interface{}{
					"seeded":     {"from": "precall"},
					"coder_bash": {"from": "precall"},
				}
			},
		},
	})
	spec := AgentTurnSpec{
		ToolInit: map[string]map[string]interface{}{
			"coder_bash": {"from": "spec"},
		},
	}
	current := map[string]interface{}{
		"existing": map[string]interface{}{"from": "config"},
	}
	merged := mergeAgentToolInit(ctx, current, spec, []string{"repo:owner/name"}, nil)

	if got := merged["existing"].(map[string]interface{})["from"]; got != "config" {
		t.Fatalf("existing entry mutated: %v", got)
	}
	// The spec's explicit init wins over the PreCallInit hook for the same key.
	if got := merged["coder_bash"].(map[string]interface{})["from"]; got != "spec" {
		t.Fatalf("spec ToolInit should win: got %v", got)
	}
	// The PreCallInit hook fills a key the spec did not set.
	if got := merged["seeded"].(map[string]interface{})["from"]; got != "precall" {
		t.Fatalf("PreCallInit should seed absent key: got %v", got)
	}
	// The caller's map is never mutated in place.
	if _, ok := current["seeded"]; ok {
		t.Fatal("mergeAgentToolInit mutated the input map")
	}
}

func TestMergeAgentToolInitNoop(t *testing.T) {
	ctx := context.Background()
	current := map[string]interface{}{"a": map[string]interface{}{"x": 1}}
	merged := mergeAgentToolInit(ctx, current, AgentTurnSpec{}, nil, nil)
	if len(merged) != len(current) {
		t.Fatalf("no-op merge changed the config: %v", merged)
	}
}
