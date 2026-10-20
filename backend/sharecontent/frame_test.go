package sharecontent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactJSONCensorsSharedFrame ensures websocket frames delivered to shared
// viewers are stripped of tool call parameters, tool outputs and meta data.
func TestRedactJSONCensorsSharedFrame(t *testing.T) {
	frame := []byte(`{"type":"new_partial_message","content":{"message":{"role":"assistant","content":"hello","meta_data":{"kubeconfig":"apiVersion: v1","token":"sk-secret"},"tool_calls":[{"id":"1","function":{"name":"run_command","arguments":"{\"cmd\":\"kubectl get secrets\"}"}}]}}}`)

	out := string(RedactJSON(frame, ModeCensored))

	for _, leak := range []string{"kubectl get secrets", "kubeconfig", "apiVersion", "sk-secret"} {
		if strings.Contains(out, leak) {
			t.Fatalf("censored frame leaked %q: %s", leak, out)
		}
	}
	// The gist of what is going on must still be visible.
	for _, keep := range []string{"new_partial_message", "hello", "run_command"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("censored frame dropped %q: %s", keep, out)
		}
	}
}

// TestRedactJSONStillMasksSecretsInFullMode documents that even the "unsecure"
// share level never exposes hard credentials.
func TestRedactJSONStillMasksSecretsInFullMode(t *testing.T) {
	frame := []byte(`{"type":"new_partial_message","content":{"message":{"role":"assistant","content":"ok","meta_data":{"api_key":"super-secret"}}}}`)

	out := string(RedactJSON(frame, ModeFull))
	if strings.Contains(out, "super-secret") {
		t.Fatalf("full mode leaked a secret: %s", out)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("full mode dropped regular content: %s", out)
	}
}

// TestRedactJSONFailsClosedOnInvalidInput ensures unparseable frames are never
// forwarded verbatim.
func TestRedactJSONFailsClosedOnInvalidInput(t *testing.T) {
	out := string(RedactJSON([]byte(`{"token":"sk-secret"`), ModeCensored))
	if strings.Contains(out, "sk-secret") {
		t.Fatalf("invalid frame leaked its payload: %s", out)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("fallback frame is not valid JSON: %s", out)
	}
}
