package bots

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/apierror"
)

// TestWriteCreateInteractionErrorEmitsJSONEnvelope pins the contract the Live
// Runtime Canary depends on (open-chat-go-ci#865): every rejection of
// POST .../bots/<bot>/create-interaction must carry a JSON body with an `error`
// code, never an empty or plain-text body.
//
// Before this change the generic 500 path wrote a bodyless response, so the
// canary's `response.json()` died with
// `json.decoder.JSONDecodeError: Expecting value: line 1 column 1 (char 0)`
// and masked the real server error.
func TestWriteCreateInteractionErrorEmitsJSONEnvelope(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		status   int
		wantCode string
	}{
		{
			name:     "message required",
			err:      errInteractionMessageRequired,
			status:   http.StatusBadRequest,
			wantCode: "message_is_required",
		},
		{
			name:     "invalid interaction config",
			err:      &interactionConfigError{message: "child snapshot uuid is unknown"},
			status:   http.StatusBadRequest,
			wantCode: "invalid_interaction_config",
		},
		{
			name:     "wrapped internal error",
			err:      fmt.Errorf("create interaction: %w", errors.New("dispatch backend exploded")),
			status:   http.StatusInternalServerError,
			wantCode: "failed_to_create_interaction",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeCreateInteractionError(rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content-type = %q, want application/json", ct)
			}

			body := rec.Body.Bytes()
			if len(body) == 0 {
				t.Fatal("empty response body: the canary cannot parse this (ci#865)")
			}

			var env apierror.Response
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("body %q is not valid JSON: %v", body, err)
			}
			if env.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", env.Code, tc.wantCode)
			}
			if env.Error == "" {
				t.Fatal("error message must not be empty")
			}
		})
	}
}

// TestWriteCreateInteractionErrorDoesNotLeakInternalDetail keeps the internal
// 500 envelope generic: the underlying error is logged server-side, not echoed
// to the client.
func TestWriteCreateInteractionErrorDoesNotLeakInternalDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCreateInteractionError(rec, errors.New("dsn user:pass@tcp(db:3306)/prod"))

	var env apierror.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if env.Error == "" || env.Code != "failed_to_create_interaction" {
		t.Fatalf("unexpected envelope %+v", env)
	}
}

// TestCreateInteractionErrorNeverEmptyBody guards the generic 500 path: an
// unmapped error must still produce a parseable JSON envelope, because the
// canary calls response.json() regardless of the status code.
func TestCreateInteractionErrorNeverEmptyBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCreateInteractionError(rec, errors.New("dial tcp 10.0.0.5:3306: connect: connection refused"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty body for unmapped internal error (ci#865 regression)")
	}

	var env apierror.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q is not valid JSON: %v", rec.Body.String(), err)
	}
	if env.Code != "failed_to_create_interaction" {
		t.Fatalf("code = %q, want failed_to_create_interaction", env.Code)
	}
	// The body must not leak infrastructure detail in the generic message. The
	// underlying cause is echoed in the opt-in `detail` field instead, so the
	// canary can name it while the stable `error` field stays generic (ci#865
	// follow-up).
	if strings.Contains(env.Error, "3306") || strings.Contains(env.Error, "10.0.0.5") {
		t.Fatalf("internal error detail leaked to the client: %q", env.Error)
	}

	var detailed apierror.ResponseWithDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detailed); err != nil {
		t.Fatalf("body %q is not valid JSON: %v", rec.Body.String(), err)
	}
	if detailed.Code != "failed_to_create_interaction" {
		t.Fatalf("code = %q, want failed_to_create_interaction", detailed.Code)
	}
	if !strings.Contains(detailed.Detail, "3306") || !strings.Contains(detailed.Detail, "10.0.0.5") {
		t.Fatalf("detail should surface the underlying cause, got %q", detailed.Detail)
	}
}
