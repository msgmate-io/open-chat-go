package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// coder_context tool (ci#680): the token/usage report an agent (and the turn
// loop) can read to self-throttle before the provider context window is
// exhausted (ci#654). The numbers themselves live in the core's context-budget
// bookkeeping; the tools package reaches it through ContextUsageLookup, which is
// the same indirection RunCallbackExecutor uses (the tools package cannot import
// the msgmate package it is embedded in).
type CoderContextInput struct {
	// ChatUUID optionally reports another chat; the current chat is the default.
	ChatUUID string `json:"chat_uuid,omitempty"`
}

// ContextUsageLookup returns the context-budget usage report of a chat and
// whether it was measured (a chat that has not produced a request yet - or an
// empty chat id - only reports the effective budgets).
var ContextUsageLookup func(chatUUID string) (map[string]interface{}, bool)

var CoderContextToolDef = ToolDefinition{
	Name:        "coder_context",
	Description: "Report the context/token budget of this chat: characters used vs. the budget, an estimated token count, how many tool results were truncated or evicted, and the effective per-tool output limits. Call it before large dumps (a full-repo coder_list, a whole-file coder_read, an unfiltered coder_bash) to self-throttle instead of overflowing the model context window.",
	InputType:   CoderContextInput{},
	// RequiresInit: the tool reads the chat identity from the trusted runtime
	// init the core injects into every tool of a chat turn.
	RequiresInit:   true,
	RequiredParams: []string{},
	Parameters: map[string]interface{}{
		"chat_uuid": map[string]interface{}{"type": "string", "description": "Optional chat UUID to report; defaults to the current chat."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		in, _ := input.(CoderContextInput)
		chatUUID := strings.TrimSpace(in.ChatUUID)
		if chatUUID == "" {
			chatUUID = strings.TrimSpace(chatUUIDFromInit(init))
		}
		if ContextUsageLookup == nil {
			return "", fmt.Errorf("context usage reporting is unavailable in this deployment")
		}
		report, measured := ContextUsageLookup(chatUUID)
		if len(report) == 0 {
			return "", fmt.Errorf("no context usage available for this chat")
		}
		report["chat_uuid"] = chatUUID
		report["measured"] = measured
		if !measured {
			report["note"] = "No request has been measured for this chat yet; the values below are the effective budgets. The report is written after every provider request of a turn."
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	},
}

// chatUUIDFromInit reads the chat identity from the tool init data. The core
// injects `_runtime.chat_uuid` (and a flat `chat_uuid`) into every tool of a
// chat turn; both are accepted so a direct caller keeps working.
func chatUUIDFromInit(init map[string]interface{}) string {
	if init == nil {
		return ""
	}
	if chatUUID, ok := init["chat_uuid"].(string); ok && strings.TrimSpace(chatUUID) != "" {
		return chatUUID
	}
	runtime, ok := init["_runtime"].(map[string]interface{})
	if !ok {
		return ""
	}
	chatUUID, _ := runtime["chat_uuid"].(string)
	return chatUUID
}
