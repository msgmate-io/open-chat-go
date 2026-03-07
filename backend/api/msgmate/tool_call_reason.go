package msgmate

import (
	"encoding/json"
	"strings"
)

// ToolCallReasonKeys are the optional argument keys the model may use to
// describe *why* it is making a tool call. The first key that is present with a
// non-empty string value wins. The keys are deliberately forgiving (ci#723):
// the goal is that a coder turn reads like a narrated session instead of a bare
// stack of opaque call rows, not that the model hits one exact key.
var ToolCallReasonKeys = []string{"reason", "call_reason", "why"}

// ToolCallReasonMaxChars caps how much of a call reason is surfaced; a runaway
// model must not be able to paste a whole essay into a call row.
const ToolCallReasonMaxChars = 400

// ExtractToolCallReason pulls the optional human-readable "why this call"
// description out of an already parsed tool input (ci#723). It accepts the
// parsed map shape (what Tool.ParseArguments returns for most tools), a JSON
// string (the raw arguments of a tool whose input type is a string) or the raw
// json.RawMessage. Anything else — or a missing/blank/non-string value — yields
// an empty string, so a call without a reason keeps its previous rendering.
func ExtractToolCallReason(toolInput interface{}) string {
	var fields map[string]interface{}
	switch value := toolInput.(type) {
	case map[string]interface{}:
		fields = value
	case string:
		if err := json.Unmarshal([]byte(value), &fields); err != nil {
			return ""
		}
	case json.RawMessage:
		if err := json.Unmarshal(value, &fields); err != nil {
			return ""
		}
	default:
		return ""
	}
	if fields == nil {
		return ""
	}
	for _, key := range ToolCallReasonKeys {
		raw, exists := fields[key]
		if !exists {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		return truncateCallReason(text)
	}
	return ""
}

// truncateCallReason cuts a reason at the rune boundary so a multi-byte
// character is never split in half.
func truncateCallReason(text string) string {
	runes := []rune(text)
	if len(runes) <= ToolCallReasonMaxChars {
		return text
	}
	return strings.TrimSpace(string(runes[:ToolCallReasonMaxChars])) + "…"
}

// IsCoderToolName reports whether a tool belongs to the coding integration
// (native coder). Only those tools advertise the optional `reason` argument, so
// unrelated tools (admin actions, MCP tools, dynamic REST tools) keep the exact
// schema they had before (ci#723).
func IsCoderToolName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "coder_")
}

// withToolCallReasonProperty adds the optional `reason` property to a tool's
// input schema unless the tool already declares a property of that name. The
// property is never added to `required`, so a model that omits it behaves
// exactly as before.
func withToolCallReasonProperty(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return schema
	}
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		properties = map[string]interface{}{}
	}
	if _, exists := properties["reason"]; exists {
		schema["properties"] = properties
		return schema
	}
	properties["reason"] = map[string]interface{}{
		"type": "string",
		"description": "Optional. One short sentence (present tense) describing why you are running this tool " +
			"and what you expect to learn from it. It is shown to the human next to the call in the chat.",
	}
	schema["properties"] = properties
	return schema
}
