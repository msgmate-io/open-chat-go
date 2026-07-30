package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// requestWithDB mirrors how BackendRouting attaches the connection to the
// request context before the auth middleware runs.
func requestWithDB(r *http.Request, db *gorm.DB) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), "db", db))
}

// The middleware in this package guards every /api/v1 route, and generated
// clients (plus the live-runtime canary) call response.json() on the response
// regardless of the status code. A plain-text middleware rejection therefore
// surfaces as a JSONDecodeError instead of the actual HTTP error
// (open-chat-go-ci#865). These tests pin the JSON contract on the reject path.

func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder) APIErrorResponse {
	t.Helper()
	contentType := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("expected a JSON content type on the reject path, got %q (body %q)", contentType, rec.Body.String())
	}
	var payload APIErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("expected a JSON error body, got %q: %v", rec.Body.String(), err)
	}
	if payload.Error == "" {
		t.Fatalf("expected a non-empty error message, got %q", rec.Body.String())
	}
	return payload
}

func TestAuthMiddlewareWithoutDatabaseReturnsJSONError(t *testing.T) {
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the request must not reach the wrapped handler without a database")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/chats/some-chat-uuid", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	payload := decodeAPIError(t, rec)
	if payload.Code != "database_unavailable" {
		t.Fatalf("expected code database_unavailable, got %q", payload.Code)
	}
}

func TestAuthMiddlewareUnauthenticatedReturnsJSONForbidden(t *testing.T) {
	// resolveUserFromBearerToken and sessionTokensFromRequest both bail out
	// before touching the database for a credential-less request, so an empty
	// handle is enough to exercise the reject path.
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the request must not reach the wrapped handler without credentials")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/chats/some-chat-uuid", nil)
	req = requestWithDB(req, &gorm.DB{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	payload := decodeAPIError(t, rec)
	if payload.Code != "forbidden" {
		t.Fatalf("expected code forbidden, got %q", payload.Code)
	}
}
