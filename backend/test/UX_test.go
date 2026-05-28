package test

import (
	"backend/api/reference"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionHandlerReturnsVersionJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reference/version", nil)
	rec := httptest.NewRecorder()

	reference.VersionHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}

	var response reference.VersionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Version == "" {
		t.Fatal("expected non-empty version")
	}
}
