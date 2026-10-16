package msgmate

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
)

// Some providers/proxies do not map a model's tool call onto the structured
// `tool_calls` field and instead serialize it inline in the assistant content.
// Two serializations are seen in practice:
//
//   - DeepSeek DSML control tokens (handled in dsml.go); and
//   - the Anthropic/XML function-call shape, where the call is written as
//
//     <function_calls>
//     <invoke name="coder_edit">
//     <parameter name="path">notes.txt</parameter>
//     <parameter name="new_string">hello</parameter>
//     </invoke>
//     </function_calls>
//
// The native coder only understood the structured field, so an inline call was
// stored verbatim as the assistant text and the tool loop finished without ever
// running the requested tool. containsInlineToolCall detects both shapes and
// parseInlineToolCalls recovers the call(s) so the loop can continue.

// anthropicInvokeRe matches one <invoke name="...">...</invoke> block.
var anthropicInvokeRe = regexp.MustCompile(`(?s)<invoke\s+name="([^"]+)"\s*>(.*?)</invoke>`)

// anthropicParameterRe matches one <parameter name="...">value</parameter>
// element. The value is unescaped (entities decoded) before use.
var anthropicParameterRe = regexp.MustCompile(`(?s)<parameter\s+name="([^"]+)"\s*>(.*?)</parameter>`)

// containsInlineToolCall reports whether content carries an inline tool-call
// serialization the structured parser did not consume (DeepSeek DSML or the
// Anthropic/XML function-call shape).
func containsInlineToolCall(content string) bool {
	return containsDSMLMarker(content) || containsAnthropicInvokeMarker(content)
}

// containsAnthropicInvokeMarker reports whether content carries an Anthropic/XML
// inline function call. It requires the opening `<invoke name="...">` (or the
// `<function_calls>` wrapper) so ordinary assistant prose that merely mentions
// XML is not mistaken for a tool call.
func containsAnthropicInvokeMarker(content string) bool {
	if content == "" {
		return false
	}
	return strings.Contains(content, "<function_calls") ||
		strings.Contains(content, "<invoke name=")
}

// parseInlineToolCalls recovers inline tool calls from either serialization. It
// returns only calls whose function name is present in toolMap. An empty result
// means a block was present but could not be recovered (the caller then fails
// the turn with a clear error).
func parseInlineToolCalls(content string, toolMap map[string]Tool) []dsmlToolCall {
	if calls := parseDSMLToolCalls(content, toolMap); len(calls) > 0 {
		return calls
	}
	return parseAnthropicToolCalls(content, toolMap)
}

// parseAnthropicToolCalls recovers tool calls from an Anthropic/XML inline
// function-call block. Each `<invoke name="X">` becomes one call whose
// arguments are the JSON object built from its `<parameter>` elements.
func parseAnthropicToolCalls(content string, toolMap map[string]Tool) []dsmlToolCall {
	if !containsAnthropicInvokeMarker(content) {
		return nil
	}
	var calls []dsmlToolCall
	for _, invoke := range anthropicInvokeRe.FindAllStringSubmatch(content, -1) {
		name := strings.TrimSpace(invoke[1])
		if name == "" || isDSMLTokenName(name) {
			continue
		}
		if _, ok := toolMap[name]; !ok {
			continue
		}
		args := anthropicParameterJSON(invoke[2])
		calls = append(calls, dsmlToolCall{ToolName: name, Arguments: args})
	}
	return calls
}

// anthropicParameterJSON renders the `<parameter>` elements inside one invoke
// body into a JSON object string. Parameters are emitted as strings (the tool
// input layer coerces them), preserving order-independent semantics.
func anthropicParameterJSON(body string) string {
	args := map[string]string{}
	for _, param := range anthropicParameterRe.FindAllStringSubmatch(body, -1) {
		key := strings.TrimSpace(param[1])
		if key == "" {
			continue
		}
		args[key] = html.UnescapeString(param[2])
	}
	if len(args) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
