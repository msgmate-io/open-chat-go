package chats

import "strings"

// ShareContentMode controls how much of a shared interaction (chat) is exposed
// to the people a chat was shared with.
//
// The goal of this file is that it should never be possible to leak sensitive
// data (tool call parameters, tool outputs, secrets, tokens, kubeconfigs, ...)
// through a shared chat, no matter which API endpoint or websocket broadcast a
// viewer uses.
type ShareContentMode string

const (
	// ShareContentModeCensored only exposes the gist of what is going on: the
	// raw text content of messages, who sent them, the state of the interaction
	// and the *names* of the tool calls that happened. Tool call parameters,
	// tool results, meta data, reasoning and attachments are stripped.
	ShareContentModeCensored ShareContentMode = "censored"

	// ShareContentModeFull is the "unsecure" mode: it exposes everything that is
	// stored for the chat, except for values that are hard secrets (credentials,
	// tokens, keys) which are always masked.
	ShareContentModeFull ShareContentMode = "full"
)

// ShareContentRedactedPlaceholder is substituted for every value that is hidden
// from the viewer. It intentionally carries no information about the original
// value (no length, no prefix, no type).
const ShareContentRedactedPlaceholder = "[redacted]"

// maxRedactionDepth bounds the recursion so that hostile/looping payloads can
// not turn redaction into a denial of service.
const maxRedactionDepth = 32

// shareCensoredKeys are keys that are stripped in ShareContentModeCensored.
// They are matched against a normalized key name (lower case, no separators).
var shareCensoredKeys = map[string]struct{}{
	// tool call parameters
	"arguments": {}, "args": {}, "params": {}, "parameters": {}, "input": {}, "inputs": {},
	"toolarguments": {}, "toolargs": {}, "toolinput": {},
	// tool call results / outputs
	"result": {}, "results": {}, "output": {}, "outputs": {}, "toolresult": {}, "toolresults": {},
	"tooloutput": {}, "stdout": {}, "stderr": {}, "returnvalue": {}, "response": {},
	// message / interaction meta data
	"meta": {}, "metadata": {}, "meta_data": {}, "metainfo": {}, "reasoning": {}, "thoughts": {},
	"thinking": {}, "attachments": {}, "files": {}, "fileattachments": {},
	// code / diffs produced by tools
	"diff": {}, "patch": {}, "changes": {}, "filediff": {}, "snippets": {},
	// model configuration: system prompts and tool schemas
	"config": {}, "sharedconfig": {}, "systemprompt": {}, "prompt": {},
	"tools": {}, "tooldefinitions": {}, "toolschemas": {},
}

// shareAlwaysSecretKeys are masked even in ShareContentModeFull, because their
// values are credentials and must never be exposed through a share, an API or a
// websocket broadcast.
var shareAlwaysSecretKeys = map[string]struct{}{
	"apikey": {}, "apitoken": {}, "accesstoken": {}, "refreshtoken": {}, "idtoken": {},
	"authorization": {}, "authheader": {}, "bearer": {}, "bearertoken": {},
	"password": {}, "passwd": {}, "passphrase": {}, "secret": {}, "clientsecret": {},
	"privatekey": {}, "signingkey": {}, "encryptionkey": {}, "kubeconfig": {},
	"cookie": {}, "cookies": {}, "sessiontoken": {}, "credentials": {}, "awssecretaccesskey": {},
	"serviceaccounttoken": {}, "sshprivatekey": {}, "webhooksecret": {},
}

// NormalizeShareContentMode maps arbitrary user input onto a supported share
// content mode. Unknown/empty input defaults to the safe censored mode so that
// a missing or misspelled value can never widen what is exposed.
func NormalizeShareContentMode(raw string) ShareContentMode {
	switch ShareContentMode(strings.ToLower(strings.TrimSpace(raw))) {
	case ShareContentModeFull:
		return ShareContentModeFull
	default:
		return ShareContentModeCensored
	}
}

// IsValidShareContentMode reports whether the given raw value is an explicitly
// supported share content mode (used for input validation).
func IsValidShareContentMode(raw string) bool {
	switch ShareContentMode(strings.ToLower(strings.TrimSpace(raw))) {
	case ShareContentModeFull, ShareContentModeCensored:
		return true
	default:
		return false
	}
}

// NormalizeShareKey normalizes a payload key for lookups: lower cased and with
// separators removed so that "api_key", "apiKey" and "API-KEY" are equivalent.
func NormalizeShareKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		switch r {
		case '_', '-', ' ', '.', '/', '\\', ':':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RedactMessageForShare returns a copy of the given message payload that is safe
// to hand to a share viewer. The input map is never modified.
//
// In ShareContentModeCensored only the text content, the identity/timing fields
// and the names of tool calls survive; in ShareContentModeFull everything except
// hard secrets (credentials, tokens, keys) is kept.
func RedactMessageForShare(message map[string]any, mode ShareContentMode) map[string]any {
	if message == nil {
		return nil
	}
	mode = NormalizeShareContentMode(string(mode))
	redacted, ok := redactShareValue(message, mode, 0).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return applyCensoredMessageRules(redacted, mode)
}

// RedactMessagesForShare applies RedactMessageForShare to a whole message list.
func RedactMessagesForShare(messages []map[string]any, mode ShareContentMode) []map[string]any {
	if messages == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, RedactMessageForShare(message, mode))
	}
	return out
}

// RedactToolCallsForShare reduces tool calls to their name plus an explicit
// redaction marker, so a viewer can see which tools ran but never with which
// parameters or what they returned.
func RedactToolCallsForShare(toolCalls []any, mode ShareContentMode) []any {
	if toolCalls == nil {
		return nil
	}
	mode = NormalizeShareContentMode(string(mode))
	if mode == ShareContentModeFull {
		redacted, _ := redactShareValue(toolCalls, mode, 0).([]any)
		return redacted
	}
	out := make([]any, 0, len(toolCalls))
	for _, call := range toolCalls {
		name := shareToolCallName(call)
		entry := map[string]any{"redacted": true}
		if name != "" {
			entry["name"] = name
		}
		out = append(out, entry)
	}
	return out
}

// shareToolCallName extracts the tool name from the supported tool call shapes
// without ever touching the (sensitive) parameter payload.
func shareToolCallName(call any) string {
	switch typed := call.(type) {
	case map[string]any:
		for _, key := range []string{"name", "tool_name", "toolName", "function_name", "tool"} {
			if value, ok := typed[key]; ok {
				if name, ok := value.(string); ok && name != "" {
					return name
				}
			}
		}
		if fn, ok := typed["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return name
			}
		}
	case string:
		return typed
	}
	return ""
}

// redactShareValue walks an arbitrary decoded JSON value and removes everything
// that must not leave the server for the given share mode.
func redactShareValue(value any, mode ShareContentMode, depth int) any {
	if depth > maxRedactionDepth {
		return ShareContentRedactedPlaceholder
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if IsAlwaysSecretShareKey(key) {
				out[key] = ShareContentRedactedPlaceholder
				continue
			}
			if mode == ShareContentModeCensored {
				if isCensoredShareKey(key) {
					continue
				}
				// Tool calls keep their names but lose parameters/results.
				if normalized := NormalizeShareKey(key); normalized == "toolcalls" || normalized == "toolcall" {
					out[key] = RedactToolCallsForShare(item, mode)
					continue
				}
			}
			out[key] = redactShareValue(item, mode, depth+1)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactShareValue(item, mode, depth+1))
		}
		return out
	case string:
		if LooksLikeShareSecret(typed) {
			return ShareContentRedactedPlaceholder
		}
		return typed
	default:
		return value
	}
}

// IsAlwaysSecretShareKey reports whether a payload key holds a credential that
// must be masked in every share mode.
func IsAlwaysSecretShareKey(key string) bool {
	_, found := shareAlwaysSecretKeys[NormalizeShareKey(key)]
	return found
}

func isCensoredShareKey(key string) bool {
	_, found := shareCensoredKeys[NormalizeShareKey(key)]
	return found
}

// LooksLikeShareSecret performs a cheap heuristic check for credential material
// that ends up inside free form strings (log output, tool results, ...). It is
// deliberately conservative: false positives only cost readability, false
// negatives leak data.
func LooksLikeShareSecret(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	lowered := strings.ToLower(trimmed)
	for _, marker := range []string{
		"-----begin private key-----",
		"-----begin rsa private key-----",
		"-----begin openssl private key-----",
		"-----begin encrypted private key-----",
		"kubeconfig",
		"apiVersion: v1\nclusters:",
		"aws_secret_access_key",
		"aws_access_key_id",
	} {
		if strings.Contains(lowered, strings.ToLower(marker)) {
			return true
		}
	}
	for _, prefix := range []string{
		"sk-", "ghp_", "gho_", "ghs_", "github_pat_", "xoxb-", "xoxp-", "glpat-",
		"bearer ", "basic ", "eyj", // JWT
	} {
		if strings.HasPrefix(lowered, prefix) {
			return true
		}
	}
	return false
}

// applyCensoredMessageRules removes message content that is not conversation
// text but the raw result of a tool execution. In censored mode a viewer may
// see *that* a tool ran and which tool it was, never what it returned.
func applyCensoredMessageRules(message map[string]any, mode ShareContentMode) map[string]any {
	if mode != ShareContentModeCensored {
		return message
	}
	role, _ := message["role"].(string)
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "tool", "function", "tool_result", "toolresult":
		message["content"] = ShareContentRedactedPlaceholder
	}
	if _, hasToolCallID := message["tool_call_id"]; hasToolCallID && role != "user" {
		message["content"] = ShareContentRedactedPlaceholder
	}
	return message
}
