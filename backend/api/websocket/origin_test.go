package websocket

import (
	"backend/runtimecfg"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebsocketOriginPatternsFromCORSConfig(t *testing.T) {
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"CORS_ALLOWED_ORIGINS": {Value: "https://admin.example.com, https://panel.example.com:8443"},
	})
	t.Cleanup(func() { runtimecfg.SetAll(map[string]runtimecfg.Value{}) })

	patterns := websocketOriginPatterns()
	if len(patterns) != 2 {
		t.Fatalf("expected 2 origin patterns, got %v", patterns)
	}
	if patterns[0] != "admin.example.com" || patterns[1] != "panel.example.com:8443" {
		t.Fatalf("unexpected origin patterns: %v", patterns)
	}
}

func TestWebsocketOriginPatternsEmptyWhenUnconfigured(t *testing.T) {
	runtimecfg.SetAll(map[string]runtimecfg.Value{})
	if patterns := websocketOriginPatterns(); len(patterns) != 0 {
		t.Fatalf("expected no origin patterns when unconfigured, got %v", patterns)
	}
}

// websocketHandshakeRequest builds a valid WebSocket upgrade request so that
// acceptConnection progresses past protocol verification to the origin check.
func websocketHandshakeRequest(target, host, origin string) *http.Request {
	req := httptest.NewRequest("GET", target, nil)
	req.Host = host
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

// acceptOriginStatus runs the accept path and reports whether the origin check
// passed. httptest.ResponseRecorder cannot hijack, so a request that clears the
// origin check fails later at the hijack step (501); a request rejected by the
// origin check fails with 403 before that.
func acceptOriginStatus(handler *WebSocketHandler, req *http.Request) int {
	rec := httptest.NewRecorder()
	_, err := handler.acceptConnection(rec, req)
	if err == nil {
		return rec.Code
	}
	return rec.Code
}

func TestAcceptConnectionOriginGateWithoutAllowlist(t *testing.T) {
	runtimecfg.SetAll(map[string]runtimecfg.Value{})
	t.Cleanup(func() { runtimecfg.SetAll(map[string]runtimecfg.Value{}) })

	handler := NewWebSocketHandler()

	t.Run("same origin passes origin check", func(t *testing.T) {
		req := websocketHandshakeRequest("http://open-chat.example/ws/connect", "open-chat.example", "http://open-chat.example")
		if code := acceptOriginStatus(handler, req); code == http.StatusForbidden {
			t.Fatalf("expected same-origin to pass the origin check, got 403")
		}
	})

	t.Run("cross origin rejected without allowlist", func(t *testing.T) {
		req := websocketHandshakeRequest("http://open-chat.example/ws/connect", "open-chat.example", "http://other.example")
		if code := acceptOriginStatus(handler, req); code != http.StatusForbidden {
			t.Fatalf("expected cross-origin to be rejected with empty allowlist, got %d", code)
		}
	})
}

func TestAcceptConnectionOriginGateWithAllowlist(t *testing.T) {
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"CORS_ALLOWED_ORIGINS": {Value: "https://admin.example.com"},
	})
	t.Cleanup(func() { runtimecfg.SetAll(map[string]runtimecfg.Value{}) })

	handler := NewWebSocketHandler()

	t.Run("allowlisted origin passes origin check", func(t *testing.T) {
		req := websocketHandshakeRequest("http://open-chat.example/ws/connect", "open-chat.example", "https://admin.example.com")
		if code := acceptOriginStatus(handler, req); code == http.StatusForbidden {
			t.Fatalf("expected allowlisted origin to pass the origin check, got 403")
		}
	})

	t.Run("same origin still passes alongside allowlist", func(t *testing.T) {
		req := websocketHandshakeRequest("http://open-chat.example/ws/connect", "open-chat.example", "http://open-chat.example")
		if code := acceptOriginStatus(handler, req); code == http.StatusForbidden {
			t.Fatalf("expected same-origin to remain allowed, got 403")
		}
	})

	t.Run("non-allowlisted cross origin rejected", func(t *testing.T) {
		req := websocketHandshakeRequest("http://open-chat.example/ws/connect", "open-chat.example", "https://evil.example.com")
		if code := acceptOriginStatus(handler, req); code != http.StatusForbidden {
			t.Fatalf("expected non-allowlisted origin to be rejected, got %d", code)
		}
	})
}

