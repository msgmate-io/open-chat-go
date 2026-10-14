package msgmate

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	tooldefs "backend/api/msgmate/tools"
)

// streamingToolCall renders a minimal SSE body that makes the tool loop invoke
// a single named tool with the given JSON arguments.
func streamingToolCall(t *testing.T, name, arguments string) string {
	t.Helper()
	chunk := map[string]interface{}{
		"choices": []map[string]interface{}{{
			"delta": map[string]interface{}{
				"tool_calls": []map[string]interface{}{{
					"id":   "call-391",
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
		}},
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to encode tool-call chunk: %v", err)
	}
	return "data: " + string(encoded) + "\n\ndata: [DONE]\n\n"
}

func runToolLoopOnce(t *testing.T, def tooldefs.ToolDefinition) *toolCallResult {
	t.Helper()
	tool := NewToolFromDefinition(def)
	reader := bufio.NewReader(strings.NewReader(streamingToolCall(t, def.Name, "{}")))
	chunkChan := make(chan string, 8)
	usageChan := make(chan *TokenUsage, 4)
	toolChan := make(chan ToolCall, 16)
	result, err := processStreamingResponseReader(
		reader,
		map[string]Tool{def.Name: tool},
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("processStreamingResponseReader returned error: %v", err)
	}
	return result
}

// TestResolveConfirmationAutonomousCoderAskNeverPending guards ci#391: a tool
// with a call-time resolver (as the coder integration supplies for coder_ask)
// must not produce a pending_confirmation in an autonomous session. The core
// uses the resolver's answer and continues.
func TestResolveConfirmationAutonomousCoderAskNeverPending(t *testing.T) {
	def := tooldefs.ToolDefinition{
		Name:                           "coder_ask",
		RequiresConfirmation:           true,
		StopOnFirstConfirmableToolCall: true,
		ConfirmationBlockMessage:       "Waiting for the user's answer.",
		InputType:                      map[string]interface{}{},
		ResolveConfirmation: func(input interface{}, init map[string]interface{}) (bool, string) {
			return false, "Autonomous mode: proceeding with your own recommended choices."
		},
		RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
			t.Fatal("the confirmable tool body must not run when a resolver auto-resolves the call")
			return "", nil
		},
	}

	result := runToolLoopOnce(t, def)
	if result.status != ToolCallStatusSucceeded {
		t.Fatalf("autonomous coder_ask status = %q, want %q", result.status, ToolCallStatusSucceeded)
	}
	if result.status == ToolCallStatusPendingConfirmation {
		t.Fatalf("autonomous coder_ask must never be pending_confirmation")
	}
	if result.result != "Autonomous mode: proceeding with your own recommended choices." {
		t.Fatalf("autonomous coder_ask result = %q, want the resolver answer", result.result)
	}
	if result.stopAfterTool {
		t.Fatalf("autonomous coder_ask must not stop the turn")
	}
}

// TestResolveConfirmationInteractiveCoderAskStillPending is the control: a
// resolver that keeps the requirement leaves the core's confirmation gate
// intact, so an interactive coder_ask still yields a pending_confirmation.
func TestResolveConfirmationInteractiveCoderAskStillPending(t *testing.T) {
	def := tooldefs.ToolDefinition{
		Name:                           "coder_ask",
		RequiresConfirmation:           true,
		StopOnFirstConfirmableToolCall: true,
		ConfirmationBlockMessage:       "Waiting for the user's answer.",
		InputType:                      map[string]interface{}{},
		ResolveConfirmation: func(input interface{}, init map[string]interface{}) (bool, string) {
			return true, ""
		},
		RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
			t.Fatal("a confirmable tool body must not run while pending")
			return "", nil
		},
	}

	result := runToolLoopOnce(t, def)
	if result.status != ToolCallStatusPendingConfirmation {
		t.Fatalf("interactive coder_ask status = %q, want %q", result.status, ToolCallStatusPendingConfirmation)
	}
	if !result.stopAfterTool {
		t.Fatalf("interactive coder_ask must stop the turn after the confirmable call")
	}
}

// TestResolveConfirmationWithoutResolverFallsBackToStaticFlag verifies the hook
// is opt-in: a tool without a resolver keeps the previous static behavior.
func TestResolveConfirmationWithoutResolverFallsBackToStaticFlag(t *testing.T) {
	def := tooldefs.ToolDefinition{
		Name:                 "coder_write",
		RequiresConfirmation: true,
		InputType:            map[string]interface{}{},
		RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
			return "ran", nil
		},
	}
	result := runToolLoopOnce(t, def)
	if result.status != ToolCallStatusPendingConfirmation {
		t.Fatalf("static confirmable status = %q, want %q", result.status, ToolCallStatusPendingConfirmation)
	}
}
