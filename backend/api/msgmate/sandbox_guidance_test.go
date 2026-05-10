package msgmate

import (
	"strings"
	"testing"
)

// ci#739: a `coder_*` result whose output shows the sandbox was never
// provisioned must reach the model with explicit readiness guidance, so the
// agent re-runs the profile setup instead of "fixing" imports or giving up.
func TestAppendExecutedToolCallsFramesSandboxProvisioningFailures(t *testing.T) {
	calls := []executedToolCall{
		{
			id:        "call-a",
			name:      "coder_bash",
			arguments: `{"command":"go test ./..."}`,
			result:    "go: github.com/msgmate-io/private-integration: module lookup disabled by GOPRIVATE",
			status:    ToolCallStatusFailed,
		},
		{
			id:        "call-b",
			name:      "coder_read",
			arguments: `{"path":"backend/go.mod"}`,
			result:    "sh: 1: go: not found",
			status:    ToolCallStatusFailed,
		},
	}

	_, messages, _ := appendExecutedToolCalls(nil, calls, map[string]bool{}, ResolveToolResultLimits(nil))
	if len(messages) != 4 {
		t.Fatalf("expected assistant+tool message per call, got %d", len(messages))
	}

	bashResult, _ := messages[1]["content"].(string)
	if !strings.Contains(bashResult, "not materialized") {
		t.Fatalf("coder_bash module failure must carry provisioning guidance, got %q", bashResult)
	}
	if !strings.Contains(bashResult, "openchat-integrations") {
		t.Fatalf("guidance must name the setup command, got %q", bashResult)
	}

	// `go` was not on PATH but the workspace was fine: the guidance must point
	// at the toolchain location rather than blaming the integration modules.
	readResult, _ := messages[3]["content"].(string)
	if !strings.Contains(readResult, "/usr/local/go/bin") {
		t.Fatalf("missing toolchain must be explained, got %q", readResult)
	}
}

func TestAppendExecutedToolCallsLeavesRealTestFailuresAlone(t *testing.T) {
	calls := []executedToolCall{
		{
			id:        "call-a",
			name:      "coder_bash",
			arguments: `{"command":"go test ./api/msgmate/"}`,
			result:    "--- FAIL: TestParseToolCalls (0.01s)\n    openai_test.go:42: want 2 tool calls, got 1",
			status:    ToolCallStatusFailed,
		},
	}

	_, messages, _ := appendExecutedToolCalls(nil, calls, map[string]bool{}, ResolveToolResultLimits(nil))
	if len(messages) != 2 {
		t.Fatalf("expected assistant+tool message, got %d", len(messages))
	}
	result, _ := messages[1]["content"].(string)
	if !strings.Contains(result, "want 2 tool calls, got 1") {
		t.Fatalf("the failing assertion must stay visible, got %q", result)
	}
	if strings.Contains(result, "not materialized") || strings.Contains(result, "/usr/local/go/bin") {
		t.Fatalf("a genuine test failure must not be reframed as a sandbox problem: %q", result)
	}
}
