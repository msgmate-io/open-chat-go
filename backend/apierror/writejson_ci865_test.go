package apierror

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ci#865: the bot surface answers success payloads through WriteJSON (and
// rejections through Write), so a generated client — or the Live Runtime
// Canary — that calls response.json() on the answer sees the real JSON body
// (or the real JSON error envelope) instead of a JSONDecodeError on an
// empty/plain-text body.
func TestWriteJSONEmitsJSONBody(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := map[string]interface{}{
		"chat_uuid": "3faca383-0000-4000-8000-000000000001",
		"ok":        true,
	}

	if !WriteJSON(rec, http.StatusOK, payload) {
		t.Fatalf("WriteJSON returned false for a healthy writer")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("WriteJSON returned an empty body")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON (%q): %v", rec.Body.String(), err)
	}
	if body["chat_uuid"] != payload["chat_uuid"] {
		t.Fatalf("body = %+v, want chat_uuid %v", body, payload["chat_uuid"])
	}
}

func TestWriteJSONToleratesNilWriter(t *testing.T) {
	if WriteJSON(nil, http.StatusOK, map[string]string{"ok": "true"}) {
		t.Fatalf("WriteJSON(nil, ...) must report failure")
	}
}

// The canary must be able to tell the two failure classes apart: bot identity
// (resolve) vs interaction/backend (create-interaction). Every rejection has to
// carry the machine readable code in the JSON body.
func TestWriteProducesMachineReadableCode(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, http.StatusInternalServerError, "failed_to_create_interaction", "Failed to create interaction")

	var body Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON (%q): %v", rec.Body.String(), err)
	}
	if body.Code != "failed_to_create_interaction" {
		t.Fatalf("code = %q, want failed_to_create_interaction", body.Code)
	}
}
