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
//     by the owner; hard-secret-looking keys and values are still masked so a
//     share can never emit credentials, tokens or kubeconfig material.
//
// Anything that is not an explicit "unsecure" is treated as censored, which
// keeps the system fail-closed for legacy rows and unknown input.
package sharecensor

import (
	"encoding/json"
	"regexp"
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
		return maskSecretValue(v)
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
	for _, needle := range secretKeyNeedles {
		if strings.Contains(lk, needle) {
			return true
		}
	}
	return false
}

// secretKeyNeedles are key substrings whose values are credential material and
// must never be rendered on any shared surface.
var secretKeyNeedles = []string{"token", "secret", "password", "api_key", "apikey",
	"credential", "kubeconfig", "kube_config", "authorization", "cookie", "email",
	"private_key", "ssh_key", "passwd"}

// hardSecretKeyNeedles is the credential-only subset of secretKeyNeedles that is
// masked even in the "unsecure" content mode. Free-form user data keys (email,
// ...) are only stripped by the censored reduction, because "unsecure" is an
// explicit owner opt-in to expose the chat content.
var hardSecretKeyNeedles = []string{"token", "secret", "password", "api_key", "apikey",
	"credential", "kubeconfig", "kube_config", "authorization", "private_key",
	"ssh_key", "passwd", "cookie", "session_id", "set-cookie"}

// isHardSecretKey reports whether a lower-cased key names credential material
// that must never be shared, not even on an "unsecure" share.
func isHardSecretKey(lk string) bool {
	for _, needle := range hardSecretKeyNeedles {
		if strings.Contains(lk, needle) {
			return true
		}
	}
	return false
}

// secretValuePrefixes are the well-known credential value shapes that are masked
// wherever they appear, even inside free text.
var secretValuePrefixes = []string{"-----begin ", "ghp_", "github_pat_", "glpat-",
	"xoxb-", "xoxp-", "sk-", "akia", "aiza", "bearer ", "eyj", "basic "}

// secretValuePattern matches credential material embedded anywhere inside a
// string (provider tokens, private key headers, bearer headers, JWTs).
var secretValuePattern = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`ghp_[a-z0-9]{16,}`,
	`github_pat_[a-z0-9_]{16,}`,
	`glpat-[a-z0-9_\-]{16,}`,
	`xox[bpasr]-[a-z0-9\-]{10,}`,
	`sk-[a-z0-9]{16,}`,
	`akia[0-9a-z]{16}`,
	`aiza[0-9a-z_\-]{20,}`,
	`bearer\s+[a-z0-9\-._~+/]{12,}`,
	`basic\s+[a-z0-9+/]{16,}=*`,
	`eyj[a-z0-9_\-]{8,}\.[a-z0-9_\-]{8,}\.[a-z0-9_\-]{4,}`,
	`-----begin [a-z ]*private key-----`,
}, "|"))

// LooksLikeSecret reports whether a raw string looks like credential material
// (private key block, provider token, JWT or bearer header).
func LooksLikeSecret(value string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return false
	}
	for _, prefix := range secretValuePrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return strings.Contains(trimmed, "-----begin ") && strings.Contains(trimmed, "private key")
}

// redactEmbeddedSecrets replaces credential-shaped substrings inside free text
// with the redaction placeholder while keeping the surrounding text readable.
func redactEmbeddedSecrets(value string) string {
	return secretValuePattern.ReplaceAllString(value, RedactedPlaceholder)
}

// maskSecretValue replaces credential-shaped strings with the redaction
// placeholder and scrubs credentials embedded in free text; other values pass
// through untouched.
func maskSecretValue(v interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return v
	}
	if LooksLikeSecret(s) {
		return RedactedPlaceholder
	}
	return redactEmbeddedSecrets(s)
}

// MaskSecretsJSON rewrites an encoded JSON document so credential-shaped keys
// and values are replaced by RedactedPlaceholder. Unlike CensorSharedJSON it
// keeps the document shape, so it can also be applied to the "unsecure" content
// mode: no share may ever emit tokens, passwords, kubeconfig material or private
// keys. Non-JSON input is returned unchanged.
func MaskSecretsJSON(raw []byte) []byte {
	var doc interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	out, err := json.Marshal(maskSecretsDeep(doc))
	if err != nil {
		return raw
	}
	return out
}

func maskSecretsDeep(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for k, val := range typed {
			if isHardSecretKey(strings.ToLower(k)) {
				out[k] = RedactedPlaceholder
				continue
			}
			out[k] = maskSecretsDeep(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			out = append(out, maskSecretsDeep(item))
		}
		return out
	default:
		return maskSecretValue(v)
	}
}
