package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAllTasksCompletedToolDefinitionAndResult(t *testing.T) {
	if AllTasksCompletedToolDef.Name != "all_tasks_completed" {
		t.Fatalf("unexpected tool name %q", AllTasksCompletedToolDef.Name)
	}
	if len(AllTasksCompletedToolDef.RequiredParams) != 1 || AllTasksCompletedToolDef.RequiredParams[0] != "summary" {
		t.Fatalf("summary must be required, got %#v", AllTasksCompletedToolDef.RequiredParams)
	}

	out, err := AllTasksCompletedToolDef.RunFunction(AllTasksCompletedInput{
		Summary: "Implemented the tool and tests",
		Blocked: true,
	}, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid result JSON %q: %v", out, err)
	}
	if payload["completed"] != true || payload["blocked"] != true {
		t.Fatalf("unexpected completion payload %#v", payload)
	}
	if payload["summary"] != "Implemented the tool and tests" {
		t.Fatalf("unexpected summary %#v", payload["summary"])
	}

	if _, err := AllTasksCompletedToolDef.RunFunction(AllTasksCompletedInput{}, nil); err == nil {
		t.Fatal("empty summary must be rejected")
	}
	if strings.TrimSpace(AllTasksCompletedToolDef.Description) == "" {
		t.Fatal("tool description must explain when to call it")
	}
}
