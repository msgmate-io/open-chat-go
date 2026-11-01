package apierror

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ci#865: generated clients (and the Live Runtime Canary) call response.json()
// regardless of the status code. Every rejection must therefore carry a JSON
// body, otherwise the client dies with a JSONDecodeError and the real HTTP
// status/body is never reported.
func TestWriteAlwaysEmitsJSONBody(t *testing.T) {
	cases := []struct {
		status int
		code   string
		msg    string
	}{
		{http.StatusBadRequest, "message_is_required", "message is required"},
		{http.StatusNotFound, "bot_not_found", "Bot not found"},
		{http.StatusConflict, "ambiguous_bot_identifier", "ambiguous bot identifier"},
		{http.StatusForbidden, "access_denied_to_file_attachment", "Access denied to file attachment"},
		{http.StatusInternalServerError, "failed_to_create_interaction", "Failed to create interaction"},
	}

	for _, tc := range cases {
		rec := httptest.NewRecorder()
		Write(rec, tc.status, tc.code, tc.msg)

		if rec.Code != tc.status {
			t.Fatalf("status = %d, want %d", rec.Code, tc.status)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q, want application/json", got)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("status %d returned an empty body", tc.status)
		}

		var body Response
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("status %d body is not JSON (%q): %v", tc.status, rec.Body.String(), err)
		}
		if body.Error != tc.msg || body.Code != tc.code {
			t.Fatalf("body = %+v, want {Error:%q Code:%q}", body, tc.msg, tc.code)
		}
	}
}

// ci#865 success-path side of the same contract: the envelope-aware writer used
// for bot-surface 200s must always produce a parseable JSON body. An empty 200
// (or one cut off before the JSON starts) is indistinguishable to a generated
// client from a vanished response, so it must never be emitted by WriteJSON.
func TestWriteJSONAlwaysEmitsParseableBody(t *testing.T) {
	payloads := []interface{}{
		map[string]interface{}{
			"success":      true,
			"chat_uuid":    "chat-1",
			"message_uuid": "msg-1",
		},
		struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{ID: "bot-1", Name: "msgmate-coder-bot"},
	}

	for _, tc := range payloads {
		rec := httptest.NewRecorder()
		var got any
		if !WriteJSON(rec, http.StatusOK, tc) {
			t.Fatalf("WriteJSON reported failure for payload %#v", tc)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q, want application/json", got)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("200 returned an empty body")
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("200 body is not JSON (%q): %v", rec.Body.String(), err)
		}
	}
}

func TestWriteJSONNilWriterReportsFailure(t *testing.T) {
	if WriteJSON(nil, http.StatusOK, map[string]interface{}{"ok": true}) {
		t.Fatalf("WriteJSON with a nil writer must report failure")
	}
}
