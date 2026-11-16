package msgmate

import (
	"encoding/json"
	"strings"
)

// ci#1116: a turn that pauses on a confirmable tool call must leave the model
// context valid and append-only, so approving the call resumes the same
// interaction instead of starting a new task and instead of invalidating the
// provider's cached prefix.
//
// Two things used to break that:
//
//  1. a parked tool call has no persisted result yet, so the rebuilt history
//     emitted an assistant `tool_calls` message without its mandatory `tool`
//     counterpart. Providers reject or silently repair that shape, and the
//     replay re-issued the call - which is where the duplicated
//     "Waiting for your confirmation ..." rounds came from.
//  2. the generated confirmation prompt ("Waiting for your confirmation to run
//     `coder_bash`.") is UI text for clients that do not render the inline
//     confirm widget, but it was replayed as a separate assistant turn sitting
//     between the tool call and its result, so the transcript prefix changed.
//
// The helpers below keep the transcript shape fixed: one `tool` message per call
// in the assistant payload, always in the same slot, carrying either the stored
// result or a deterministic placeholder that tells the agent a confirmation is
// in flight. The generated prompt never enters the model context.

// ConfirmablePausePromptMetaKey marks a persisted message whose text is the
// generated confirmable-pause fallback rather than model output. The chat still
// shows it; the model context must not.
const ConfirmablePausePromptMetaKey = "confirmable_pause_prompt"

// confirmablePausePromptPrefix is the stable opening of the generated fallback
// text. It lets rows persisted before ConfirmablePausePromptMetaKey existed be
// recognised as synthetic too.
const confirmablePausePromptPrefix = "Waiting for your confirmation"

// isConfirmablePausePrompt reports whether a persisted message text is the
// generated confirmable-pause fallback instead of real assistant output. Both
// the explicit metadata flag (new rows) and the legacy shape (a message that
// carries confirmable_actions and whose text opens with the generated prefix)
// are recognised.
func isConfirmablePausePrompt(meta *map[string]interface{}, text string) bool {
	if meta != nil {
		if flag, ok := (*meta)[ConfirmablePausePromptMetaKey].(bool); ok && flag {
			return true
		}
		if actions, ok := (*meta)["confirmable_actions"].([]interface{}); ok && len(actions) > 0 {
			if strings.HasPrefix(strings.TrimSpace(text), confirmablePausePromptPrefix) {
				return true
			}
		}
	}
	return false
}

// pendingConfirmationToolResult is the context result for a call that is parked
// awaiting the user's approval. It must stay byte-identical for the whole pause
// (that is what keeps the token prefix cacheable) and it must make the pause
// legible to the model, so the agent waits for the approval instead of
// re-issuing the call or restating the task.
func pendingConfirmationToolResult(toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "the requested tool"
	} else {
		name = "`" + name + "`"
	}
	return "Confirmation required: " + name + " has NOT run yet. The proposal is parked in the chat confirmation widget and the user has been asked to approve it. Do not re-issue this call and do not restate the task; approving resumes this interaction with the call's real result."
}

// toolResultForContext resolves the context text for one persisted tool call.
//
// It always returns a non-empty payload, because the assistant `tool_calls`
// message it belongs to must keep its `tool` counterpart: a dangling tool call
// is what made the post-confirmation turn invalid and un-cacheable.
func toolResultForContext(toolCallMap map[string]interface{}) string {
	name, _ := toolCallMap["name"].(string)
	status, _ := toolCallMap["status"].(string)

	resultText := ""
	switch typedResult := toolCallMap["result"].(type) {
	case string:
		resultText = typedResult
	case nil:
	default:
		if encodedResult, err := json.Marshal(typedResult); err == nil {
			resultText = string(encodedResult)
		}
	}
	if strings.TrimSpace(resultText) != "" {
		return resultText
	}

	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending_confirmation", "pending", "awaiting_confirmation":
		return pendingConfirmationToolResult(name)
	case "failed", "rejected", "denied", "cancelled", "cancelled_by_user":
		if reason, _ := toolCallMap["error"].(string); strings.TrimSpace(reason) != "" {
			return "The call did not run: " + strings.TrimSpace(reason)
		}
		return "The call did not run: it was " + strings.TrimSpace(status) + "."
	}
	return "The call returned no output."
}
