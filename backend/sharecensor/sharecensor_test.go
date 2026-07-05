package sharecensor

import (
	"encoding/json"
	"testing"
)

func TestNormalizeContentModeDefaultsToCensored(t *testing.T) {
	for _, raw := range []string{"", "  ", "nope", "CENSORED", "censored"} {
		if got := NormalizeContentMode(raw); got != ContentModeCensored {
			t.Fatalf("NormalizeContentMode(%q) = %q, want %q", raw, got, ContentModeCensored)
		}
		if !IsCensored(raw) {
			t.Fatalf("IsCensored(%q) = false, want true", raw)
		}
	}
	if got := NormalizeContentMode(" Unsecure "); got != ContentModeUnsecure {
		t.Fatalf("NormalizeContentMode(unsecure) = %q", got)
	}
	if IsCensored(ContentModeUnsecure) {
		t.Fatal("unsecure mode must not be censored")
	}
}

func TestCensorToolCallsKeepsOnlyNames(t *testing.T) {
	calls := []interface{}{
		map[string]interface{}{
			"name":      "run_command",
			"arguments": map[string]interface{}{"command": "kubectl get secrets -o yaml"},
			"result":    "super-secret-token",
			"id":        "call_1",
		},
	}
	censored := CensorToolCalls(&calls)
	if censored == nil || len(*censored) != 1 {
		t.Fatalf("unexpected censored tool calls: %#v", censored)
	}
	encoded, err := json.Marshal(*censored)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	if serialized != "[{\"name\":\"run_command\"}]" {
		t.Fatalf("tool call leaked extra fields: %s", serialized)
	}
	for _, secret := range []string{"kubectl", "secret", "call_1", "arguments", "result"} {
		if contains(serialized, secret) {
			t.Fatalf("censored tool call leaked %q: %s", secret, serialized)
		}
	}
}

func TestCensorMetaDataDropsUnknownKeys(t *testing.T) {
	meta := map[string]interface{}{
		"model":        "gpt-4o",
		"state":        "running",
		"kubeconfig":   "apiVersion: v1",
		"access_token": "abc",
		"user_email":   "a@b.c",
	}
	censored := CensorMetaData(&meta)
	if censored == nil {
		t.Fatal("expected censored meta data")
	}
	if _, ok := (*censored)["kubeconfig"]; ok {
		t.Fatal("kubeconfig leaked")
	}
	if _, ok := (*censored)["access_token"]; ok {
		t.Fatal("token leaked")
	}
	if _, ok := (*censored)["user_email"]; ok {
		t.Fatal("user data leaked")
	}
	if (*censored)["model"] != "gpt-4o" || (*censored)["state"] != "running" {
		t.Fatalf("safe interaction state lost: %#v", *censored)
	}
	if empty := CensorMetaData(&map[string]interface{}{"secret": "x"}); empty != nil {
		t.Fatal("expected nil when nothing safe remains")
	}
}

func TestCensorWebSocketPayloadStripsSensitiveFrame(t *testing.T) {
	frame := map[string]interface{}{
		"type": "new_partial_message",
		"content": map[string]interface{}{
			"content":     "deploying now",
			"attachments": []interface{}{map[string]interface{}{"name": "kubeconfig"}},
			"tool_calls": []interface{}{map[string]interface{}{
				"name":      "apply_manifest",
				"arguments": map[string]interface{}{"token": "secret"},
				"result":    "ok",
			}},
			"meta_data": map[string]interface{}{"api_key": "secret", "state": "running"},
			"reasoning": []interface{}{"secret chain of thought"},
		},
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	censored := string(CensorWebSocketPayload(raw))
	if contains(censored, "secret") || contains(censored, "kubeconfig") || contains(censored, "api_key") {
		t.Fatalf("censored frame leaked secrets: %s", censored)
	}
	if !contains(censored, "deploying now") || !contains(censored, "apply_manifest") {
		t.Fatalf("censored frame lost the gist: %s", censored)
	}

	control := []byte(`{"type":"interaction_state","content":{"state":"running"}}`)
	if string(CensorWebSocketPayload(control)) != string(control) {
		t.Fatal("control frames must pass through untouched")
	}
	if string(CensorWebSocketPayload([]byte("not json"))) != "not json" {
		t.Fatal("non-json payloads must pass through untouched")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= 0 && len(needle) > 0 && stringsContains(haystack, needle)
}

func TestMaskSecretsJSONKeepsShapeButMasksCredentials(t *testing.T) {
	raw := []byte(`{"content":{"text":"run kubectl with ghp_abcdefghijklmnopqrstuvwxyz012345",
		"meta_data":{"state":"running","api_key":"plain","cookie":"session=abc"},
		"tool_calls":[{"name":"run_command","arguments":{"command":"ls"},"result":"ok"}],
		"authorization":"Bearer abcdefghijklmnopqrstuvwxyz"}}`)
	masked := string(MaskSecretsJSON(raw))
	for _, leaked := range []string{"ghp_", "Bearer abc", `"api_key":"plain"`, "session=abc", `"authorization":"Bearer`} {
		if contains(masked, leaked) {
			t.Fatalf("unsecure mask leaked %q: %s", leaked, masked)
		}
	}
	// Free-form content and the interaction state stay visible so the gist of the
	// interaction is still readable.
	for _, kept := range []string{"run kubectl with", `"state":"running"`, "run_command"} {
		if !contains(masked, kept) {
			t.Fatalf("unsecure mask dropped %q: %s", kept, masked)
		}
	}
	if string(MaskSecretsJSON([]byte("not json"))) != "not json" {
		t.Fatal("non-json payloads must pass through untouched")
	}
	if !LooksLikeSecret("-----BEGIN OPENSSH PRIVATE KEY-----") || LooksLikeSecret("hello world") {
		t.Fatal("LooksLikeSecret misclassifies credential material")
	}
}

func stringsContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
