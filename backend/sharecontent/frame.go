package sharecontent

import "encoding/json"

// redactedFrameFallback is delivered instead of any payload that cannot be
// decoded. Failing closed matters more than completeness: an unparseable frame
// is never forwarded as-is.
const redactedFrameFallback = `{"type":"redacted","content":{"redacted":true}}`

// RedactJSON redacts an already serialized JSON payload, e.g. a websocket
// frame that is broadcast to shared interaction viewers.
//
// Websocket frames carry the same sensitive fields as the HTTP responses
// (meta_data, tool call parameters, tool outputs, reasoning, ...), so every
// frame delivered to a shared subscriber has to pass through the same rules.
// Decoding and re-encoding keeps this working for frame types that are added
// later without having to touch this function.
func RedactJSON(raw []byte, mode Mode) []byte {
	if len(raw) == 0 {
		return raw
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return []byte(redactedFrameFallback)
	}

	out, err := json.Marshal(RedactValue(decoded, NormalizeMode(string(mode)), 0))
	if err != nil {
		return []byte(redactedFrameFallback)
	}
	return out
}
