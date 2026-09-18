package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"backend/database"
	"backend/runtimecfg"
)

type capturedProxyRequest struct {
	Host   string
	Origin string
	Path   string
}

type proxyRequestRecorder struct {
	mu       sync.Mutex
	requests []capturedProxyRequest
}

func (rec *proxyRequestRecorder) record(r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.requests = append(rec.requests, capturedProxyRequest{
		Host:   r.Host,
		Origin: r.Header.Get("Origin"),
		Path:   r.URL.Path,
	})
}

func (rec *proxyRequestRecorder) last(t *testing.T) capturedProxyRequest {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.requests) == 0 {
		t.Fatalf("upstream received no requests")
	}
	return rec.requests[len(rec.requests)-1]
}

func withMobileProxyRuntimeConfig(t *testing.T, upstreamURL string) {
	t.Helper()
	previous := runtimecfg.GetAll()
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	next := make(map[string]runtimecfg.Value, len(previous)+2)
	for key, value := range previous {
		next[key] = value
	}
	next["MOBILE_ROUTE_API_WS_TO_UPSTREAM"] = runtimecfg.Value{Value: "true"}
	next["MOBILE_UPSTREAM_URL"] = runtimecfg.Value{Value: upstreamURL}
	runtimecfg.SetAll(next)
}

func TestMobileProxyRewritesWebSocketUpgradeOrigin(t *testing.T) {
	recorder := &proxyRequestRecorder{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	withMobileProxyRuntimeConfig(t, upstream.URL)

	config := setupServerTestDB(t)
	DB := database.SetupDatabase(*config)
	router, _ := BackendRouting(DB, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), false, "", "", false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/ssh/servers/server-1/shell/ws", nil)
	req.Host = "localhost:1984"
	req.Header.Set("Origin", "http://localhost:1984")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")

	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, req)

	if httpRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200 from proxied upgrade, got %d", httpRecorder.Code)
	}

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("failed to parse upstream URL: %v", err)
	}

	got := recorder.last(t)
	if got.Origin != target.Scheme+"://"+target.Host {
		t.Fatalf("expected upstream Origin %q, got %q", target.Scheme+"://"+target.Host, got.Origin)
	}
	if got.Host != target.Host {
		t.Fatalf("expected upstream Host %q, got %q", target.Host, got.Host)
	}
}

func TestMobileProxyKeepsOriginForNonUpgradeRequests(t *testing.T) {
	recorder := &proxyRequestRecorder{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	withMobileProxyRuntimeConfig(t, upstream.URL)

	config := setupServerTestDB(t)
	DB := database.SetupDatabase(*config)
	router, _ := BackendRouting(DB, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), false, "", "", false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/list", nil)
	req.Host = "localhost:1984"
	req.Header.Set("Origin", "http://localhost:1984")

	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, req)

	if httpRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200 from proxied request, got %d", httpRecorder.Code)
	}

	got := recorder.last(t)
	if got.Origin != "http://localhost:1984" {
		t.Fatalf("expected original Origin to be preserved for non-upgrade, got %q", got.Origin)
	}
}

func TestRewriteMobileProxyRequestOrigin(t *testing.T) {
	target, err := url.Parse("https://msgmate.io")
	if err != nil {
		t.Fatalf("failed to parse target: %v", err)
	}

	cases := []struct {
		name       string
		upgrade    string
		origin     string
		wantOrigin string
	}{
		{name: "websocket upgrade rewritten", upgrade: "websocket", origin: "http://localhost:1984", wantOrigin: "https://msgmate.io"},
		{name: "case insensitive upgrade", upgrade: "WebSocket", origin: "http://localhost:1984", wantOrigin: "https://msgmate.io"},
		{name: "no upgrade keeps origin", upgrade: "", origin: "http://localhost:1984", wantOrigin: "http://localhost:1984"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://localhost:1984/api/v1/integrations/list", nil)
			req.Header.Set("Origin", tc.origin)
			if tc.upgrade != "" {
				req.Header.Set("Upgrade", tc.upgrade)
			}

			rewriteMobileProxyRequestOrigin(req, target)

			if got := req.Header.Get("Origin"); got != tc.wantOrigin {
				t.Fatalf("expected Origin %q, got %q", tc.wantOrigin, got)
			}
		})
	}
}

func TestRewriteMobileProxyRequestOriginIgnoresNilTarget(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://localhost:1984/api/v1/integrations/list", nil)
	req.Header.Set("Origin", "http://localhost:1984")
	req.Header.Set("Upgrade", "websocket")

	rewriteMobileProxyRequestOrigin(req, nil)

	if got := req.Header.Get("Origin"); got != "http://localhost:1984" {
		t.Fatalf("expected Origin to be untouched for nil target, got %q", got)
	}
}
