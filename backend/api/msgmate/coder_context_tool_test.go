package msgmate

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCoderContextToolIsRegisteredAndBackedByTheUsageReport pins the ci#680
// wiring: `coder_context` is constructible by name, reports the recorded usage
// report of the chat the turn is in (resolved from the trusted runtime init the
// loop injects), and degrades to the effective budgets with an explicit note
// when the chat has produced no request yet.
func TestCoderContextToolIsRegisteredAndBackedByTheUsageReport(t *testing.T) {
	resetContextUsageRegistry()

	tool, found := NewToolByName("coder_context")
	if !found || tool == nil {
		t.Fatal("expected the coder_context tool to be registered")
	}
	if tool.GetToolName() != "coder_context" || tool.GetToolType() != "function" {
		t.Fatalf("unexpected coder_context tool: %s/%s", tool.GetToolName(), tool.GetToolType())
	}
	if !tool.GetRequiresInit() {
		t.Fatal("coder_context must receive the trusted runtime init to resolve the chat")
	}

	// No measurement yet: the effective budgets are reported, with a note.
	tool.SetInitData(map[string]interface{}{
		"_runtime": map[string]interface{}{"chat_uuid": "chat-unmeasured"},
	})
	result, err := tool.RunTool(map[string]interface{}{})
	if err != nil {
		t.Fatalf("coder_context failed: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("expected a JSON payload, got %q (%v)", result, err)
	}
	if payload["measured"] != false {
		t.Fatalf("expected measured=false for an unmeasured chat, got %#v", payload)
	}
	if payload["chat_uuid"] != "chat-unmeasured" {
		t.Fatalf("expected the runtime chat id, got %#v", payload["chat_uuid"])
	}
	if payload["budget_chars"] != float64(DefaultContextBudgetChars) {
		t.Fatalf("expected the effective budget, got %#v", payload["budget_chars"])
	}
	if _, ok := payload["note"]; !ok {
		t.Fatalf("expected a note explaining the budgets-only report, got %#v", payload)
	}
	limits, ok := payload["tool_result_max_chars"].(map[string]interface{})
	if !ok || limits["default"] != float64(DefaultToolResultMaxCharsForModel) {
		t.Fatalf("expected the effective per-tool limits, got %#v", payload["tool_result_max_chars"])
	}

	// A recorded measurement is reported as-is.
	RecordContextUsage("chat-measured", ContextUsageReport{
		BudgetChars:          1000,
		UsedChars:            400,
		UsedPercent:          40,
		EstimatedTokens:      100,
		TruncatedToolResults: 2,
		Limits:               ResolveToolResultLimits(nil),
	})
	tool.SetInitData(map[string]interface{}{"_runtime": map[string]interface{}{"chat_uuid": "chat-measured"}})
	result, err = tool.RunTool(map[string]interface{}{})
	if err != nil {
		t.Fatalf("coder_context failed: %v", err)
	}
	// A fresh map: unmarshalling into the previous one would merge the earlier
	// (unmeasured) payload's keys into the new one.
	payload = map[string]interface{}{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("expected a JSON payload, got %q (%v)", result, err)
	}
	if payload["measured"] != true || payload["used_chars"] != float64(400) || payload["truncated_tool_results"] != float64(2) {
		t.Fatalf("expected the recorded report, got %#v", payload)
	}
	if _, ok := payload["note"]; ok {
		t.Fatalf("a measured chat must not carry the budgets-only note, got %#v", payload)
	}
	if payload["budget_chars"] != float64(1000) {
		t.Fatalf("expected the measured char budget, got %#v", payload["budget_chars"])
	}
	if !strings.Contains(tool.GetToolDescription(), "context") {
		t.Fatalf("expected the description to explain the report, got %q", tool.GetToolDescription())
	}
}

// TestContextBudgetMetadataShape pins the `context_budget` message metadata the
// chat renders: the ticket asks for a token/usage report an agent and the loop
// can use to self-throttle (ci#654).
func TestContextBudgetMetadataShape(t *testing.T) {
	report := BuildContextUsageReport(
		[]map[string]interface{}{
			{"role": "system", "content": "system"},
			{"role": "user", "content": "work"},
		},
		1000, ContextCompactionStats{}, ResolveToolResultLimits(nil), 0,
	)
	meta := (&TurnOutcome{Reason: TurnEndedCompleted, Context: report}).Context.Metadata()
	for _, key := range []string{
		"budget_chars", "used_chars", "used_percent", "estimated_tokens",
		"messages", "tool_result_messages", "truncated_tool_results",
		"evicted_tool_results", "dropped_messages", "tool_result_max_chars",
	} {
		if _, ok := meta[key]; !ok {
			t.Fatalf("expected the context budget metadata to carry %q, got %#v", key, meta)
		}
	}
	if meta["used_chars"] != messagesCharCount([]map[string]interface{}{
		{"role": "system", "content": "system"},
		{"role": "user", "content": "work"},
	}) {
		t.Fatalf("expected the measured usage, got %#v", meta)
	}
}
