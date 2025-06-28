package server

import (
	"backend/database"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMatchBrowserTokenRoute(t *testing.T) {
	cases := []struct {
		method    string
		path      string
		wantScope string
		wantOK    bool
	}{
		{http.MethodGet, "/bots/list", database.ScopeBotsRead, true},
		{http.MethodGet, "/api/v1/bots/list", database.ScopeBotsRead, true},
		{http.MethodGet, "/bots/some-bot-uuid", database.ScopeBotsRead, true},
		{http.MethodPatch, "/bots/some-bot-uuid", database.ScopeBotsWrite, true},
		{http.MethodPut, "/bots/some-bot-uuid/config", database.ScopeBotsWrite, true},
		{http.MethodGet, "/chats/list", database.ScopeInteractionsList, true},
		{http.MethodGet, "/chats/chat-uuid", database.ScopeInteractionsRead, true},
		{http.MethodGet, "/chats/chat-uuid/messages/list", database.ScopeInteractionsRead, true},
		{http.MethodGet, "/chats/chat-uuid/status", database.ScopeInteractionsRead, true},
		{http.MethodGet, "/api/v1/chats/chat-uuid/status", database.ScopeInteractionsRead, true},
		{http.MethodGet, "/user/self", "", true},

		// default deny
		{http.MethodPost, "/bots", "", false},
		{http.MethodDelete, "/bots/some-bot-uuid", "", false},
		{http.MethodPost, "/bots/some-bot-uuid/interactions", "", false},
		{http.MethodPost, "/chats/create", "", false},
		{http.MethodPost, "/chats/chat-uuid/messages/send", "", false},
		{http.MethodGet, "/contacts/list", "", false},
		{http.MethodPost, "/user/access-tokens", "", false},
		{http.MethodPost, "/user/browser-token", "", false},
		{http.MethodGet, "/user/permissions", "", false},
		{http.MethodGet, "/admin/users", "", false},
		{http.MethodGet, "/metrics", "", false},
		{http.MethodGet, "/integrations/list", "", false},
		{http.MethodPost, "/files/upload", "", false},
		{http.MethodGet, "/connect", "", false},
		{http.MethodGet, "/models", "", false},
		{http.MethodGet, "/api/v1/models", "", false},
		{http.MethodGet, "/chats/chat-uuid/contact", "", false},
		{http.MethodGet, "/bots/list/extra", "", false},
		{http.MethodPost, "/bots/list", "", false},
	}

	for _, tc := range cases {
		scope, ok := matchBrowserTokenRoute(tc.method, tc.path)
		if ok != tc.wantOK || scope != tc.wantScope {
			t.Errorf("matchBrowserTokenRoute(%q, %q) = (%q, %v), want (%q, %v)",
				tc.method, tc.path, scope, ok, tc.wantScope, tc.wantOK)
		}
	}
}

func TestCORSMiddlewarePreflightAndHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORSMiddleware([]string{"https://admin.example.com"})(inner)

	t.Run("allowed preflight", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/bots/list", nil)
		req.Header.Set("Origin", "https://admin.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204 preflight response, got %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.com" {
			t.Fatalf("unexpected Access-Control-Allow-Origin: %q", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
			t.Fatalf("expected Access-Control-Allow-Headers to be set")
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Fatalf("expected Vary: Origin, got %q", got)
		}
	})

	t.Run("disallowed preflight", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/bots/list", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for disallowed preflight, got %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected no Access-Control-Allow-Origin, got %q", got)
		}
	})

	t.Run("allowed simple request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/bots/list", nil)
		req.Header.Set("Origin", "https://admin.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected request to pass through, got %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.com" {
			t.Fatalf("unexpected Access-Control-Allow-Origin: %q", got)
		}
	})

	t.Run("disallowed origin gets no CORS headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/bots/list", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected server-side processing to continue, got %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected no Access-Control-Allow-Origin for disallowed origin, got %q", got)
		}
	})

	t.Run("empty allowlist never sets CORS headers", func(t *testing.T) {
		emptyHandler := CORSMiddleware(nil)(inner)
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/bots/list", nil)
		req.Header.Set("Origin", "https://admin.example.com")
		rec := httptest.NewRecorder()
		emptyHandler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected no Access-Control-Allow-Origin with empty allowlist, got %q", got)
		}
		if rec.Code == http.StatusNoContent {
			t.Fatalf("expected preflight to fall through to the wrapped handler with empty allowlist")
		}
	})

	t.Run("same-origin request without Origin header passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/bots/list", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected same-origin request to pass, got %d", rec.Code)
		}
	})
}
