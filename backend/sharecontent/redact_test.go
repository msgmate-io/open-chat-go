package sharecontent

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeModeDefaultsToCensored(t *testing.T) {
	for _, raw := range []string{"", "  ", "bogus", "CENSORED", "censored"} {
		if got := NormalizeMode(raw); got != ModeCensored {
			t.Fatalf("NormalizeMode(%q) = %q, want censored", raw, got)
		}
	}
	for _, raw := range []string{"full", "FULL", " full "} {
		if got := NormalizeMode(raw); got != ModeFull {
			t.Fatalf("NormalizeMode(%q) = %q, want full", raw, got)
		}
	}
	if IsValidMode("nope") {
		t.Fatal("expected invalid mode to be rejected")
	}
	if !IsValidMode("censored") || !IsValidMode("full") {
		t.Fatal("expected supported modes to be valid")
	}
}

func TestRedactMessageCensoredDropsToolPayloads(t *testing.T) {
	message := map[string]any{
		"content": "hello",
		"role":    "assistant",
		"meta_data": map[string]any{
			"model": "gpt-4o",
		},
		"tool_calls": []any{
			map[string]any{
				"name":      "coder_bash",
				"arguments": `{"command":"kubectl get secret -o yaml"}`,
				"result":    "very sensitive output",
			},
		},
	}

	out := RedactMessage(message, ModeCensored)
	if out["content"] != "hello" || out["role"] != "assistant" {
		t.Fatalf("raw text content must survive censoring, got %#v", out)
	}
	if _, ok := out["meta_data"]; ok {
		t.Fatal("meta_data must not be exposed in censored mode")
	}
	raw, ok := out["tool_calls"].([]any)
	if !ok || len(raw) != 1 {
		t.Fatalf("expected one redacted tool call, got %#v", out["tool_calls"])
	}
	call, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected tool call map, got %#v", raw[0])
	}
	if call["name"] != "coder_bash" {
		t.Fatalf("tool name must be visible, got %#v", call)
	}
	for _, forbidden := range []string{"arguments", "args", "result", "output"} {
		if _, present := call[forbidden]; present {
			t.Fatalf("tool call key %q leaked in censored mode", forbidden)
		}
	}
	if call["redacted"] != true {
		t.Fatalf("expected redaction marker, got %#v", call)
	}
}

func TestRedactMessageFullMasksSecretsOnly(t *testing.T) {
	message := map[string]any{
		"content": "text",
		"arguments": map[string]any{
			"path":    "main.go",
			"api_key": "sk-should-not-leak",
		},
		"note": "Bearer abcdefghijklmnop",
	}

	out := RedactMessage(message, ModeFull)
	args, ok := out["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("full mode must keep tool arguments, got %#v", out["arguments"])
	}
	if args["path"] != "main.go" {
		t.Fatalf("non-secret argument must survive, got %#v", args)
	}
	if args["api_key"] != RedactedPlaceholder {
		t.Fatalf("api_key must be masked, got %#v", args["api_key"])
	}
	if out["note"] != RedactedPlaceholder {
		t.Fatalf("bearer token must be masked, got %#v", out["note"])
	}
}

func TestRedactMessageDoesNotMutateInput(t *testing.T) {
	inner := map[string]any{"secret": "keep"}
	message := map[string]any{"arguments": inner}
	RedactMessage(message, ModeCensored)
	if _, ok := message["arguments"]; !ok {
		t.Fatal("input message must not be mutated")
	}
	if inner["secret"] != "keep" {
		t.Fatal("nested input must not be mutated")
	}
}

func TestRedactMessageHandlesNilAndDeepPayloads(t *testing.T) {
	if got := RedactMessage(nil, ModeCensored); got != nil {
		t.Fatalf("nil message must stay nil, got %#v", got)
	}
	deep := map[string]any{"content": "x"}
	for i := 0; i < MaxRedactionDepth+5; i++ {
		deep = map[string]any{"nested": deep}
	}
	if out := RedactMessage(deep, ModeCensored); out == nil {
		t.Fatal("deep payload must not panic or return nil")
	}
}

func TestLooksLikeSecret(t *testing.T) {
	secrets := []string{
		"sk-abcdefghijklmnop",
		"ghp_abcdefghijklmnop",
		"-----BEGIN PRIVATE KEY-----",
		"apiVersion: v1\nclusters: []",
	}
	for _, value := range secrets {
		if !LooksLikeSecret(value) {
			t.Fatalf("expected %q to look like a secret", value)
		}
	}
	if LooksLikeSecret("just a normal sentence") {
		t.Fatal("plain text must not be flagged")
	}
}

func TestRedactMessageCensoredHidesToolOutput(t *testing.T) {
	message := map[string]any{
		"role":         "tool",
		"tool_call_id": "call_1",
		"content":      "secret tool output",
	}
	out := RedactMessage(message, ModeCensored)
	if out["content"] != RedactedPlaceholder {
		t.Fatalf("tool output must be hidden in censored mode, got %#v", out["content"])
	}
}

func TestWriteRedactedJSONStripsCensoredFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	payload := map[string]any{
		"content_mode":        "censored",
		"config":              map[string]any{"system_prompt": "be nice"},
		"interaction_details": map[string]any{"model": "gpt-4o"},
	}
	if err := WriteRedactedJSON(recorder, payload, ModeCensored); err != nil {
		t.Fatalf("WriteRedactedJSON failed: %v", err)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "system_prompt") || strings.Contains(body, "be nice") {
		t.Fatalf("censored response leaked config: %s", body)
	}
	if !strings.Contains(body, "gpt-4o") {
		t.Fatalf("censored response must keep the gist, got: %s", body)
	}
}
