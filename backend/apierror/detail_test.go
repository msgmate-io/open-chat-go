package apierror

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ci#865: the generic create-interaction 500 must still be JSON (the canary
// calls response.json() regardless of status), and when a detail is available
// it must ride along so the canary names the real cause instead of dying in
// the decoder with an empty body.
func TestWriteWithDetailEmitsJSONBody(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteDetailed(rec, http.StatusInternalServerError, "failed_to_create_interaction", "Failed to create interaction", "no rows in result set")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	var body ResponseWithDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON (%q): %v", rec.Body.String(), err)
	}
	if body.Error != "Failed to create interaction" || body.Code != "failed_to_create_interaction" {
		t.Fatalf("body = %+v, want {Error:... Code:failed_to_create_interaction}", body)
	}
	if body.Detail != "no rows in result set" {
		t.Fatalf("detail = %q, want %q", body.Detail, "no rows in result set")
	}
}
