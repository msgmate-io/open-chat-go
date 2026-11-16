package msgmate

import (
	"strings"
	"testing"
)

func TestIsConfirmablePausePrompt(t *testing.T) {
	actions := []interface{}{map[string]interface{}{"tool": "coder_bash"}}
	tests := []struct {
		name string
		meta *map[string]interface{}
		text string
		want bool
	}{
		{"flagged row", &map[string]interface{}{ConfirmablePausePromptMetaKey: true}, "anything", true},
		{"legacy prompt with actions", &map[string]interface{}{"confirmable_actions": actions}, "Waiting for your confirmation to run `coder_bash`.", true},
		{"plain assistant text", &map[string]interface{}{"confirmable_actions": actions}, "Ledger update and ci#1093 verdict.", false},
		{"no metadata", nil, "Real answer", false},
		{"unflagged pause looking text", &map[string]interface{}{}, "Waiting for your confirmation to run `coder_bash`.", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isConfirmablePausePrompt(test.meta, test.text); got != test.want {
				t.Fatalf("isConfirmablePausePrompt() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPendingConfirmationToolResultIsStableAndNamesTheTool(t *testing.T) {
	first := pendingConfirmationToolResult("coder_bash")
	second := pendingConfirmationToolResult("coder_bash")
	if first != second {
		t.Fatalf("placeholder must be byte-identical across turns, got %q and %q", first, second)
	}
	if !strings.Contains(first, "`coder_bash`") {
		t.Fatalf("placeholder should name the parked tool, got %q", first)
	}
	if !strings.Contains(strings.ToLower(first), "not run") {
		t.Fatalf("placeholder must tell the agent the call did not run, got %q", first)
	}
	if got := pendingConfirmationToolResult(""); !strings.Contains(got, "the requested tool") {
		t.Fatalf("placeholder must degrade gracefully without a tool name, got %q", got)
	}
}

func TestToolResultForContextCompacts(t *testing.T) {
	const output = "line one\nline two"
	tests := []struct {
		name      string
		toolCall  map[string]interface{}
		want      string
		wantInfix string
	}{
		{"persisted string result", map[string]interface{}{"name": "coder_bash", "status": "succeeded", "result": output}, output, ""},
		{"structured result", map[string]interface{}{"name": "coder_read", "status": "succeeded", "result": map[string]interface{}{"ok": true}}, "", "\"ok\""},
		{"pending confirmation", map[string]interface{}{"name": "coder_bash", "status": "pending_confirmation"}, "", "Confirmation required"},
		{"awaiting confirmation", map[string]interface{}{"name": "coder_bash", "status": "awaiting_confirmation"}, "", "Confirmation required"},
		{"cancelled with reason", map[string]interface{}{"name": "coder_bash", "status": "cancelled", "error": "user declined"}, "The call did not run: user declined", ""},
		{"completed without result", map[string]interface{}{"name": "coder_bash", "status": "succeeded"}, "The call returned no output.", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := toolResultForContext(test.toolCall)
			if got == "" {
				t.Fatal("toolResultForContext must never be empty: a dangling tool_call breaks the history")
			}
			if test.want != "" && got != test.want {
				t.Fatalf("toolResultForContext() = %q, want %q", got, test.want)
			}
			if test.wantInfix != "" && !strings.Contains(got, test.wantInfix) {
				t.Fatalf("toolResultForContext() = %q, want it to contain %q", got, test.wantInfix)
			}
		})
	}
}

// The context shape of a paused turn and of the same turn after approval must be
// prefix-identical except for the parked tool result slot itself.
func TestApprovalKeepsContextPrefixStable(t *testing.T) {
	parked := []map[string]interface{}{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "run the tests"},
		{"role": "assistant", "content": pendingConfirmationToolResult("coder_bash")},
	}
	approved := append([]map[string]interface{}{}, parked...)
	approved[2] = map[string]interface{}{"role": "assistant", "content": "done"}
	for i := 0; i < 2; i++ {
		if parked[i]["content"] != approved[i]["content"] {
			t.Fatalf("message %d changed across the approval (cache prefix broken)", i)
		}
	}
}
