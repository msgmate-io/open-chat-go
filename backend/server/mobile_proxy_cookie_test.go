package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"backend/database"
	"backend/runtimecfg"
)

func withMobileProxyRuntimeConfigNamespace(t *testing.T, upstreamURL, namespace string) {
	t.Helper()
	previous := runtimecfg.GetAll()
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	next := make(map[string]runtimecfg.Value, len(previous)+3)
	for key, value := range previous {
		next[key] = value
	}
	next["MOBILE_ROUTE_API_WS_TO_UPSTREAM"] = runtimecfg.Value{Value: "true"}
	next["MOBILE_UPSTREAM_URL"] = runtimecfg.Value{Value: upstreamURL}
	next["MOBILE_SESSION_NAMESPACE"] = runtimecfg.Value{Value: namespace}
	runtimecfg.SetAll(next)
}

// TestMobileProxyRewritesSessionCookieName is the response-side counterpart of
// TestMobileSessionCookieNameMatchesAndroidClient: the Android selector reads
// the namespaced cookie out of the local WebView jar, so the proxy must rewrite
// the upstream `session_id` Set-Cookie to exactly that name and drop the
// Secure/Domain attributes (which would otherwise stop the plain-HTTP local
// origin from storing the cookie). Regression for open-chat-go-ci#276.
func TestMobileProxyRewritesSessionCookieName(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    "tok",
			Path:     "/",
			Domain:   "ci.msgmate.io",
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	const namespace = "ci"
	withMobileProxyRuntimeConfigNamespace(t, upstream.URL, namespace)

	config := setupServerTestDB(t)
	DB := database.SetupDatabase(*config)
	router, _ := BackendRouting(DB, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), false, "", "", false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", nil)
	req.Host = "127.0.0.1:1984"

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("failed to parse upstream URL: %v", err)
	}
	wantName := mobileSessionCookieName(target, namespace)

	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.HasPrefix(setCookie, wantName+"=tok") {
		t.Fatalf("expected cookie name %q, got %q", wantName, setCookie)
	}
	if strings.Contains(strings.ToLower(setCookie), "; secure") {
		t.Fatalf("expected Secure stripped, got %q", setCookie)
	}
	if strings.Contains(strings.ToLower(setCookie), "domain=") {
		t.Fatalf("expected Domain stripped, got %q", setCookie)
	}
	if !strings.Contains(setCookie, "SameSite=Lax") {
		t.Fatalf("expected SameSite preserved, got %q", setCookie)
	}
}
