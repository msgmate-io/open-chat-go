package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// coder_compact tool (ci#394 gap 7, ticket ci#957): the agent-facing surface
// of the core's context-compaction planner (backend/nativecoder/compaction.go).
// A long coder interaction can exhaust the provider context window; the
// planner produces a byte-capped, oldest-first, duplicate-dropped summary of
// the session that persists as the chat's context_summary, which the reader
// re-injects on later turns (ci#793). The tool validates and bounds the
// model's input and delegates the actual fold to the core through the
// CoderCompactRunner seam below (the tools package cannot import the
// msgmate/chats packages it is embedded in).
type CoderCompactInput struct {
	// Text is the transcript excerpt to fold into the session summary. A
	// blank text only reports the current summary with nothing folded.
	Text string `json:"text,omitempty"`
	// CapBytes bounds the summary; 0 falls back to the compact store default.
	CapBytes int `json:"cap_bytes,omitempty"`
}

// CoderCompactRunner is the core-side seam of the tool. The owning process
// publishes the per-chat compact surface through this provider (the same
// indirection ContextUsageLookup and CoderScheduleRunnerProvider use).
type CoderCompactRunner interface {
	// CompactCoderContext folds text into the bounded summary of chatUUID and
	// returns the persisted state of the summary.
	CompactCoderContext(chatUUID string, text string, capBytes int) (map[string]interface{}, error)
	// CoderContextSummary returns the current bounded summary of chatUUID.
	CoderContextSummary(chatUUID string) (map[string]interface{}, error)
}

// CoderCompactRunnerProvider returns the compact runner of the current
// deployment, or nil when no core published one. A nil provider makes the
// tool fail loudly instead of silently dropping the summary.
var CoderCompactRunnerProvider func() CoderCompactRunner

var CoderCompactToolDef = ToolDefinition{
	Name:        "coder_compact",
	Description: "Fold a transcript excerpt into this chat's bounded session summary and persist it. The summary is byte-capped (oldest material first, duplicates dropped) and is re-injected into the model context on later turns, so a long interaction survives the context window: call coder_compact(text=...) with a dense excerpt of what has already happened (decisions, files touched, remaining plan) before the window runs low, and call it with no text to read the current summary. Never pass secrets or full file contents - the summary must stay small and is not redacted.",
	InputType:   CoderCompactInput{},
	// RequiresInit: the summary always belongs to the trusted chat of the
	// running turn, which the core injects into every tool of a chat turn.
	RequiresInit:   true,
	RequiredParams: []string{},
	Parameters: map[string]interface{}{
		"text":      map[string]interface{}{"type": "string", "description": "Transcript excerpt to fold into the session summary; blank only reports the current summary."},
		"cap_bytes": map[string]interface{}{"type": "integer", "description": "Byte cap for the summary; 0 uses the store default (12000)."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		var runner CoderCompactRunner
		if CoderCompactRunnerProvider != nil {
			runner = CoderCompactRunnerProvider()
		}
		return runCoderCompact(input, init, runner)
	},
}

// runCoderCompact is the testable core of the tool: it validates the input,
// resolves the trusted chat and delegates to the runner.
func runCoderCompact(input interface{}, init map[string]interface{}, runner CoderCompactRunner) (string, error) {
	in, _ := input.(CoderCompactInput)
	if pointer, ok := input.(*CoderCompactInput); ok && pointer != nil {
		in = *pointer
	}
	chatUUID := strings.TrimSpace(chatUUIDFromInit(init))
	if chatUUID == "" {
		return "", fmt.Errorf("coder_compact needs the chat identity of the running turn")
	}
	if runner == nil {
		return "", fmt.Errorf("coder_compact is unavailable in this deployment (no compaction runner published)")
	}
	text := in.Text
	if in.CapBytes < 0 {
		return "", fmt.Errorf("coder_compact cap_bytes must be >= 0")
	}
	if strings.TrimSpace(text) == "" {
		state, err := runner.CoderContextSummary(chatUUID)
		if err != nil {
			return "", err
		}
		if len(state) == 0 {
			return "", fmt.Errorf("no context summary available for this chat")
		}
		state["chat_uuid"] = chatUUID
		encoded, err := json.Marshal(state)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	state, err := runner.CompactCoderContext(chatUUID, text, in.CapBytes)
	if err != nil {
		return "", err
	}
	state["chat_uuid"] = chatUUID
	state["folded"] = true
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
