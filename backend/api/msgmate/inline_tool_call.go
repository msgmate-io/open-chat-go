package msgmate

import (
	"encoding/json"
	"errors"
	"html"
	"regexp"
	"strings"
)

// errMalformedInlineToolCall marks an assistant turn that carried an inline
// tool-call block from which no call could be recovered. The streaming loop may
// retry such a turn once, since the emission is intermittent provider output.
var errMalformedInlineToolCall = errors.New(
	"model returned a malformed inline tool-call block in the assistant content and no tool call could be recovered",
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

// bracketToolCallRe matches one `[[tool_name key="value" ...]]` placeholder.
// The body is matched lazily up to the closing `]]` so several placeholders in
// one message are recovered individually.
var bracketToolCallRe = regexp.MustCompile(`(?s)\[\[\s*([A-Za-z_][A-Za-z0-9_]*)\s*(.*?)\]\]`)

// bracketArgumentRe matches one `key="value"` / `key='value'` / `key=value`
// pair of a placeholder body.
var bracketArgumentRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s\]]+))`)

// containsInlineToolCall reports whether content carries an inline tool-call
// serialization the structured parser did not consume (DeepSeek DSML, the
// Anthropic/XML function-call shape, or the bracket placeholder).
func containsInlineToolCall(content string, toolMap map[string]Tool) bool {
	if containsDSMLMarker(content) || containsAnthropicInvokeMarker(content) {
		return true
	}
	return bracketToolCallSuspected(content, toolMap)
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

// parseInlineToolCalls recovers inline tool calls from any supported
// serialization. It returns only calls whose function name is present in
// toolMap. An empty result means a block was present but could not be recovered
// (the caller then fails the turn with a clear error).
func parseInlineToolCalls(content string, toolMap map[string]Tool) []dsmlToolCall {
	if calls := parseDSMLToolCalls(content, toolMap); len(calls) > 0 {
		return calls
	}
	if calls := parseAnthropicToolCalls(content, toolMap); len(calls) > 0 {
		return calls
	}
	return parseBracketToolCalls(content, toolMap)
}

// bracketToolCallSuspected reports whether the content carries a `[[` that
// opens a tool-call placeholder. The identifier right after `[[` must be a
// known tool name (or a prefix of one, so a placeholder split across streaming
// deltas is recognised before it is complete); plain `[[markdown]]`-style
// prose is therefore never mistaken for a call. The streaming loop uses this to
// withhold a placeholder from the client instead of leaking it as assistant
// text (ci#562).
func bracketToolCallSuspected(content string, toolMap map[string]Tool) bool {
	if len(toolMap) == 0 {
		return false
	}
	idx := strings.Index(content, "[[")
	if idx < 0 {
		return false
	}
	rest := content[idx+2:]
	if rest == "" || rest[0] == '[' {
		// A `[[[` opener is not a placeholder.
		return false
	}
	name := rest
	if cut := strings.IndexFunc(rest, func(r rune) bool {
		return !(r == '_' ||
			r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9')
	}); cut >= 0 {
		name = rest[:cut]
	}
	if name == "" {
		return false
	}
	for key := range toolMap {
		if key == name || strings.HasPrefix(key, name) {
			return true
		}
	}
	return false
}

// parseBracketToolCalls recovers tool calls from the `[[tool_name key="value"]]`
// placeholder serialization some providers/models emit instead of a structured
// tool_calls entry. Each placeholder becomes one call whose arguments are the
// JSON object built from its `key=value` pairs; unknown tools are dropped.
func parseBracketToolCalls(content string, toolMap map[string]Tool) []dsmlToolCall {
	if !bracketToolCallSuspected(content, toolMap) {
		return nil
	}
	var calls []dsmlToolCall
	for _, match := range bracketToolCallRe.FindAllStringSubmatch(content, -1) {
		name := strings.TrimSpace(match[1])
		if name == "" {
			continue
		}
		if _, ok := toolMap[name]; !ok {
			continue
		}
		calls = append(calls, dsmlToolCall{ToolName: name, Arguments: bracketArgumentJSON(match[2])})
	}
	return calls
}

// bracketArgumentJSON renders the `key=value` pairs of a placeholder body into
// a JSON object string. Values are emitted as strings (the tool input layer
// coerces them).
func bracketArgumentJSON(body string) string {
	args := map[string]string{}
	for _, match := range bracketArgumentRe.FindAllStringSubmatch(body, -1) {
		key := strings.TrimSpace(match[1])
		if key == "" {
			continue
		}
		value := match[2]
		if value == "" {
			value = match[3]
		}
		if value == "" {
			value = match[4]
		}
		args[key] = html.UnescapeString(value)
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
