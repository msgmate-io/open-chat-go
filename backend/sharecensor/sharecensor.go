// Package sharecensor implements the fail-closed payload reduction used by
// every shared-chat surface (HTTP API responses and websocket pushes).
//
// A share carries a content mode:
//
//   - "censored" (default): only the raw message text and the tool-call names
//     survive. Tool-call arguments, tool results, message meta-data, model
//     reasoning and file attachments are stripped, so a shared view can never
//     leak secrets, tokens, kubeconfigs or user data.
//   - "unsecure": the full payload is exposed. It must be selected explicitly
//     by the owner.
//
// Anything that is not an explicit "unsecure" is treated as censored, which
// keeps the system fail-closed for legacy rows and unknown input.
package sharecensor

import (
	"encoding/json"
	"strings"
)

const (
	// ContentModeCensored exposes text and tool names only.
	ContentModeCensored = "censored"
	// ContentModeUnsecure exposes the full chat content.
	ContentModeUnsecure = "unsecure"
	// RedactedPlaceholder marks a field that was removed by censoring.
	RedactedPlaceholder = "[redacted]"
)

// metaDataAllowList lists the message meta-data keys that are safe to render on
// a censored shared view (interaction state, not content). Every other key is
// dropped.
var metaDataAllowList = []string{
	"model",
	"backend",
	"provider",
	"state",
	"status",
	"queued",
	"tokens",
	"auto_continue",
	"auto_continue_max",
}

// NormalizeContentMode maps arbitrary input to a supported content mode,
// defaulting to the censored mode.
func NormalizeContentMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), ContentModeUnsecure) {
		return ContentModeUnsecure
	}
	return ContentModeCensored
}

// IsCensored reports whether a content mode must strip sensitive payload
// fields. The zero value (empty/legacy) is censored.
func IsCensored(mode string) bool {
	return NormalizeContentMode(mode) != ContentModeUnsecure
}

// CensorToolCallMap reduces a single tool call to its name.
func CensorToolCallMap(toolCall map[string]interface{}) map[string]interface{} {
	censored := map[string]interface{}{}
	for _, key := range []string{"name", "tool", "tool_name", "function"} {
		if value, exists := toolCall[key]; exists {
			if name, ok := value.(string); ok && strings.TrimSpace(name) != "" {
				censored["name"] = name
			}
			break
		}
	}
	return censored
}

// CensorToolCalls reduces a list of tool calls to name-only entries. Arguments,
// results and provider-side identifiers are dropped.
func CensorToolCalls(toolCalls *[]interface{}) *[]interface{} {
	if toolCalls == nil {
		return nil
	}
	censored := make([]interface{}, 0, len(*toolCalls))
	for _, raw := range *toolCalls {
		mapped := toMap(raw)
		if mapped == nil {
			continue
		}
		censored = append(censored, CensorToolCallMap(mapped))
	}
	return &censored
}

// CensorMetaData keeps only the non-sensitive interaction-state keys of a
// message meta-data map. It returns nil when nothing safe remains, so the
// caller omits the field entirely.
func CensorMetaData(meta *map[string]interface{}) *map[string]interface{} {
	if meta == nil {
		return nil
	}
	censored := map[string]interface{}{}
	for _, key := range metaDataAllowList {
		if value, exists := (*meta)[key]; exists {
			censored[key] = value
		}
	}
	if len(censored) == 0 {
		return nil
	}
	return &censored
}

// CensorMetaDataMap is the map-typed variant of CensorMetaData.
func CensorMetaDataMap(meta map[string]interface{}) map[string]interface{} {
	if meta == nil {
		return nil
	}
	censored := CensorMetaData(&meta)
	if censored == nil {
		return nil
	}
	return *censored
}

// CensorWebSocketPayload censors a websocket message envelope in place. Only
// message-bearing frames are rewritten; control frames pass through untouched.
func CensorWebSocketPayload(msg []byte) []byte {
	if len(msg) == 0 {
		return msg
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal(msg, &envelope); err != nil {
		return msg
	}

	msgType, _ := envelope["type"].(string)
	if msgType != "new_message" && msgType != "new_partial_message" {
		return msg
	}

	content, ok := envelope["content"].(map[string]interface{})
	if !ok {
		return msg
	}

	// Attachments expose file ids/names and are never safe on a censored share.
	delete(content, "attachments")

	if raw, exists := content["tool_calls"]; exists {
		if list, ok := raw.([]interface{}); ok {
			content["tool_calls"] = *CensorToolCalls(&list)
		} else {
			delete(content, "tool_calls")
		}
	}

	if raw, exists := content["meta_data"].(map[string]interface{}); exists {
		if censored := CensorMetaData(&raw); censored != nil {
			content["meta_data"] = *censored
		} else {
			delete(content, "meta_data")
		}
	}

	// Model reasoning can quote secrets and file contents; it is never part of
	// the censored gist.
	if _, exists := content["reasoning"]; exists {
		content["reasoning"] = []interface{}{}
	}

	censored, err := json.Marshal(envelope)
	if err != nil {
		return msg
	}
	return censored
}

// toMap normalizes an already-decoded JSON value into a map.
func toMap(value interface{}) map[string]interface{} {
	if mapped, ok := value.(map[string]interface{}); ok {
		return mapped
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var mapped map[string]interface{}
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil
	}
	return mapped
}
