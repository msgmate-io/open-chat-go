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
