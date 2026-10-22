package msgmate

import "testing"

func TestCollectTaskCompletionReadsTheSuccessfulCall(t *testing.T) {
	calls := []interface{}{
		map[string]interface{}{
			"name":      "all_tasks_completed",
			"status":    ToolCallStatusSucceeded,
			"arguments": `{"summary":"Shipped the tool","blocked":true}`,
		},
	}
	completed, blocked, summary := collectTaskCompletion(calls)
	if !completed || !blocked {
		t.Fatalf("expected completed+blocked, got completed=%v blocked=%v", completed, blocked)
	}
	if summary != "Shipped the tool" {
		t.Fatalf("unexpected summary %q", summary)
	}
}

func TestCollectTaskCompletionIgnoresFailedCalls(t *testing.T) {
	calls := []interface{}{
		map[string]interface{}{
			"name":      "all_tasks_completed",
			"status":    ToolCallStatusFailed,
			"arguments": `{"summary":"should not stop"}`,
		},
	}
	if completed, _, _ := collectTaskCompletion(calls); completed {
		t.Fatal("a failed all_tasks_completed call must not stop the turn")
	}
}

func TestAllTasksCompletedToolIsRegistered(t *testing.T) {
	tool, found := NewToolByName("all_tasks_completed")
	if !found {
		t.Fatal("expected all_tasks_completed to be registered")
	}
	if tool.GetToolName() != "all_tasks_completed" {
		t.Fatalf("unexpected tool %q", tool.GetToolName())
	}
}
