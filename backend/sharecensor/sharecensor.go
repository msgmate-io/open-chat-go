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

// shareMetaAllowlist lists the only message meta-data keys a censored share may
// still expose. Everything else (secrets, tokens, user data, provider payloads)
// is dropped before the payload ever leaves the server.
var shareMetaAllowlist = map[string]struct{}{
	"model": {}, "state": {}, "status": {}, "type": {},
	"provider": {}, "name": {}, "title": {},
}

// CensorSharedJSON applies the censored share reduction to an encoded JSON
// document (object, array or single frame). Raw message text and tool-call names
// survive; tool-call arguments/results, meta-data, reasoning, attachments and any
// secret-looking key are removed. Non-JSON input is returned unchanged.
func CensorSharedJSON(raw []byte) []byte {
	var doc interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	out, err := json.Marshal(reduceForShare(doc))
	if err != nil {
		return raw
	}
	return out
}

func reduceForShare(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for k, val := range typed {
			lk := strings.ToLower(k)
			switch {
			case lk == "tool_calls" || lk == "toolcalls":
				out[k] = reduceToolCallList(val)
			case lk == "meta_data" || lk == "metadata":
				if reduced := reduceMetaForShare(val); reduced != nil {
					out[k] = reduced
				}
			case isShareDeniedKey(lk):
			default:
				out[k] = reduceForShare(val)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			out = append(out, reduceForShare(item))
		}
		return out
	default:
		return v
	}
}

func reduceToolCallList(v interface{}) interface{} {
	items, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, ok := entry["name"].(string)
		if !ok || name == "" {
			continue
		}
		out = append(out, map[string]interface{}{"name": name})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func reduceMetaForShare(v interface{}) interface{} {
	meta, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]interface{}, len(meta))
	for k, val := range meta {
		if _, allowed := shareMetaAllowlist[strings.ToLower(k)]; !allowed {
			continue
		}
		switch val.(type) {
		case string, float64, bool, nil:
			out[k] = val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isShareDeniedKey(lk string) bool {
	switch lk {
	case "reasoning", "reasoning_content", "thinking", "attachments", "attachment",
		"arguments", "argument", "args", "params", "parameters", "input", "inputs",
		"result", "results", "output", "outputs", "tool_call_id", "raw", "raw_response",
		"tool_result", "tool_output", "env", "environment", "config_data", "credentials":
		return true
	}
	for _, needle := range []string{"token", "secret", "password", "api_key", "apikey",
		"credential", "kubeconfig", "kube_config", "authorization", "cookie", "email",
		"private_key", "ssh_key", "passwd"} {
		if strings.Contains(lk, needle) {
			return true
		}
	}
	return false
}
