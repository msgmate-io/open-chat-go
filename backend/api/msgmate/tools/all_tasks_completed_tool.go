package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ci#809: explicit end-of-task tool for the native coder. The turn reader
// promotes a successful call into `task_completed` message metadata, which the
// in-product auto-continue decision already understands. An agent can therefore
// stop the loop deterministically with a summary instead of relying only on the
// visible `<<TASK_COMPLETE>>` sentinel or continuing to explore after the work
// is done.
type AllTasksCompletedInput struct {
	// Summary is the short human-readable account of the completed work.
	Summary string `json:"summary"`
	// Blocked marks a task that is finished only because it cannot proceed
	// without a human decision or an external unblock.
	Blocked bool `json:"blocked,omitempty"`
}

var AllTasksCompletedToolDef = ToolDefinition{
	Name:        "all_tasks_completed",
	Description: "Declare that the entire assigned task is finished. Call this exactly once at the end, with a concise summary of what was done. When blocked=true, the summary must state the exact blocker and what a human needs to do. Do not call it while work is still in progress, and do not keep exploring or issuing tools after calling it.",
	InputType:   AllTasksCompletedInput{},
	RequiredParams: []string{
		"summary",
	},
	Parameters: map[string]interface{}{
		"summary": map[string]interface{}{
			"type":        "string",
			"description": "Short summary of the completed work, or of the exact blocker when blocked=true.",
		},
		"blocked": map[string]interface{}{
			"type":        "boolean",
			"description": "Set when the task is finished only because it needs a human decision or an external unblock.",
		},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		in, ok := input.(AllTasksCompletedInput)
		if !ok {
			return "", fmt.Errorf("invalid all_tasks_completed input")
		}
		summary := strings.TrimSpace(in.Summary)
		if summary == "" {
			return "", fmt.Errorf("summary is required")
		}
		payload, err := json.Marshal(map[string]interface{}{
			"completed": true,
			"blocked":   in.Blocked,
			"summary":   summary,
		})
		if err != nil {
			return "", err
		}
		return string(payload), nil
	},
}
