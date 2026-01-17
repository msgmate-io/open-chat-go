package msgmate

import "strings"

// DeepSeek emits tool calls inline in the assistant content using its "DeepSeek
// Markup Language" (DSML) when the provider/upstream does not map them onto the
// structured tool_calls field. The control tokens are delimited by the fullwidth
// vertical line rune U+FF5C and the literal marker "DSML" (for example
// "<U+FF5C>DSML<U+FF5C>tool_calls_begin"). The function name and its JSON
// arguments are emitted as plain text between those control tokens.
//
// The native coder only understood the structured tool_calls field, so a DSML
// emission was stored verbatim as the assistant message text and the tool loop
// finished without ever running the requested tool (ci#428). We detect such
// blocks, recover the tool call when possible, and otherwise fail the turn with
// a clear error instead of silently finishing on the raw fragment.
const dsmlMarker = "\uFF5CDSML\uFF5C"

// dsmlToolCall is a tool call recovered from inline DSML content.
type dsmlToolCall struct {
	ToolName  string
	Arguments string
}

// containsDSMLMarker reports whether content carries an inline DSML (or a
// generic <|tool...|>) marker, i.e. a tool call the structured parser did not
// consume.
func containsDSMLMarker(content string) bool {
	if content == "" {
		return false
	}
	if strings.Contains(content, dsmlMarker) {
		return true
	}
	// Tolerate providers/proxies that strip the fullwidth bars but keep the
	// token name, as well as the generic <|tool...|> marker family.
	if strings.Contains(content, "tool_calls_begin") || strings.Contains(content, "tool_calls_end") {
		return true
	}
	if strings.Contains(content, "<|tool") {
		return true
	}
	return false
}

// parseDSMLToolCalls recovers tool calls from inline DSML content. It scans the
// raw content for JSON argument objects and walks backwards over the DSML
// control tokens to the function name that precedes each one. It returns only
// calls whose function name is present in toolMap. An empty result means the
// block was present but could not be recovered (the caller then fails the turn
// with a clear error).
func parseDSMLToolCalls(content string, toolMap map[string]Tool) []dsmlToolCall {
	if !containsDSMLMarker(content) {
		return nil
	}

	var calls []dsmlToolCall
	for _, span := range extractJSONObjects(content) {
		name := functionNameBefore(content, span.start)
		if name == "" || isDSMLTokenName(name) {
			continue
		}
		if _, ok := toolMap[name]; !ok {
			continue
		}
		calls = append(calls, dsmlToolCall{ToolName: name, Arguments: span.raw})
	}
	return calls
}

type jsonObjectSpan struct {
	raw   string
	start int
}

// extractJSONObjects returns each balanced JSON object found in s, respecting
// string literals and escapes so braces inside strings do not confuse the scan.
func extractJSONObjects(s string) []jsonObjectSpan {
	var spans []jsonObjectSpan
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		end, ok := matchingBrace(s, i)
		if !ok {
			continue
		}
		spans = append(spans, jsonObjectSpan{raw: s[i : end+1], start: i})
		i = end
	}
	return spans
}

// matchingBrace returns the index of the '}' that closes the '{' at start.
func matchingBrace(s string, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// functionNameBefore returns the function name that precedes the JSON object
// starting at pos. It walks backwards over whitespace, DSML control-token bytes
// (which are non-ASCII and therefore not identifier bytes) and the token names
// themselves, returning the first identifier that is not a DSML token name.
func functionNameBefore(s string, pos int) string {
	if pos > len(s) {
		return ""
	}
	i := pos
	// A DSML block between the name and its arguments contains a handful of
	// tokens; bound the walk so a pathological input cannot spin.
	for attempts := 0; attempts < 16; attempts++ {
		for i > 0 && !isIdentifierByte(s[i-1]) {
			i--
		}
		if i == 0 {
			return ""
		}
		end := i
		for i > 0 && isIdentifierByte(s[i-1]) {
			i--
		}
		name := s[i:end]
		if name == "" || name == "DSML" || name == "dsml" || isDSMLTokenName(name) {
			continue
		}
		return name
	}
	return ""
}

// dsmlTokenNames are the control-token names DeepSeek emits. When a provider
// strips the fullwidth bar delimiters these names leak into the content and must
// never be mistaken for a function name.
var dsmlTokenNames = map[string]bool{
	"tool_calls_begin": true,
	"tool_calls_end":   true,
	"tool_call_begin":  true,
	"tool_call_end":    true,
	"tool_sep":         true,
	"parameter_begin":  true,
	"parameter_end":    true,
	"tool_calls":       true,
	"tool_call":        true,
	"parameter":        true,
	"tool":             true,
	"calls":            true,
	"call":             true,
	"begin":            true,
	"end":              true,
	"sep":              true,
}

// isDSMLTokenName reports whether name is (or ends with) a DSML control-token
// name rather than a real function name.
func isDSMLTokenName(name string) bool {
	if dsmlTokenNames[name] {
		return true
	}
	for token := range dsmlTokenNames {
		if strings.HasSuffix(name, token) {
			return true
		}
	}
	return false
}

// isIdentifierByte reports whether c can appear in a tool/function name.
func isIdentifierByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '_', '.', '-':
		return true
	}
	return false
}
