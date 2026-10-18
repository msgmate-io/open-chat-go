package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCoderContextToolReportsTheChatUsage pins the ci#680 `coder_context` tool:
// it reports the chat's usage report, falls back to the trusted-runtime chat
// identity, and degrades to the effective budgets when the chat was not
// measured yet instead of failing the call.
func TestCoderContextToolReportsTheChatUsage(t *testing.T) {
	original := ContextUsageLookup
	t.Cleanup(func() { ContextUsageLookup = original })

	seen := ""
	ContextUsageLookup = func(chatUUID string) (map[string]interface{}, bool) {
		seen = chatUUID
		return map[string]interface{}{
			"budget_chars": 100,
			"used_chars":   90,
			"used_percent": 90,
		}, true
	}

	run := CoderContextToolDef.RunFunction
	init := map[string]interface{}{
		"_runtime": map[string]interface{}{"chat_uuid": "chat-from-runtime"},
	}

	result, err := run(CoderContextInput{}, init)
	if err != nil {
		t.Fatalf("coder_context failed: %v", err)
	}
	if seen != "chat-from-runtime" {
		t.Fatalf("expected the trusted-runtime chat id, got %q", seen)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("expected a JSON payload, got %q (%v)", result, err)
	}
	if payload["chat_uuid"] != "chat-from-runtime" || payload["measured"] != true {
		t.Fatalf("expected the chat id and measured=true, got %#v", payload)
	}
	if payload["used_percent"] != float64(90) {
		t.Fatalf("expected the usage numbers, got %#v", payload)
	}

	// A flat chat_uuid in init works too (a direct caller).
	if _, err := run(CoderContextInput{}, map[string]interface{}{"chat_uuid": "chat-flat"}); err != nil {
		t.Fatalf("coder_context failed: %v", err)
	}
	if seen != "chat-flat" {
		t.Fatalf("expected the flat chat id, got %q", seen)
	}

	// An explicit chat id in the input wins over the runtime identity.
	if _, err := run(CoderContextInput{ChatUUID: "chat-explicit"}, init); err != nil {
		t.Fatalf("coder_context failed: %v", err)
	}
	if seen != "chat-explicit" {
		t.Fatalf("expected the explicit chat id, got %q", seen)
	}

	// An unmeasured chat still reports the effective budgets, with a note.
	ContextUsageLookup = func(string) (map[string]interface{}, bool) {
		return map[string]interface{}{"budget_chars": 120000}, false
	}
	result, err = run(CoderContextInput{ChatUUID: "chat-new"}, init)
	if err != nil {
		t.Fatalf("coder_context failed for an unmeasured chat: %v", err)
	}
	if !strings.Contains(result, "effective budgets") {
		t.Fatalf("expected a note that only the budgets are known, got %q", result)
	}

	if _, err := run(CoderContextInput{}, nil); err != nil {
		t.Fatalf("coder_context must accept missing init: %v", err)
	}

	// Without a backing lookup the tool reports the gap instead of lying.
	ContextUsageLookup = nil
	if _, err := run(CoderContextInput{}, init); err == nil {
		t.Fatal("expected an error when no context usage reporting is wired")
	}
}

// TestCoderContextToolIsRegisteredAsANativeCoderTool pins that the tool is
// declared with a function type and an init requirement, so the loop can run it
// for a native coder agent.
func TestCoderContextToolIsRegisteredAsANativeCoderTool(t *testing.T) {
	if CoderContextToolDef.Name != "coder_context" {
		t.Fatalf("unexpected tool name %q", CoderContextToolDef.Name)
	}
	if !CoderContextToolDef.RequiresInit {
		t.Fatal("coder_context must receive the trusted runtime init to resolve the chat")
	}
	if !strings.Contains(CoderContextToolDef.Description, "context") {
		t.Fatalf("expected the description to explain the context report, got %q", CoderContextToolDef.Description)
	}
	if _, ok := CoderContextToolDef.Parameters["chat_uuid"]; !ok {
		t.Fatalf("expected an optional chat_uuid parameter, got %#v", CoderContextToolDef.Parameters)
	}
}
