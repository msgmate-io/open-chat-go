package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ci#1150: /sign-up is not a core page; when no integration contributes it the
// SPA fallback serves the tracked landing shell instead of falling through to
// /404.
func TestSignUpLandingShellMapping(t *testing.T) {
	cases := []struct {
		requestPath    string
		wantOK         bool
		wantRoute      string
		wantPathEnding string
	}{
		{requestPath: "/sign-up", wantOK: true, wantRoute: "/", wantPathEnding: "index.html"},
		{requestPath: "/sign-up/", wantOK: true, wantRoute: "/", wantPathEnding: "index.html"},
		{requestPath: "/sign-up/index.pageContext.json", wantOK: true, wantRoute: "/", wantPathEnding: "index.pageContext.json"},
		{requestPath: "/sign-up.pageContext.json", wantOK: true, wantRoute: "/", wantPathEnding: "index.pageContext.json"},
		{requestPath: "/", wantOK: false},
		{requestPath: "/login", wantOK: false},
		{requestPath: "/sign-up/confirm", wantOK: false},
		{requestPath: "/integrations/account_management/sign-up", wantOK: false},
	}

	for _, tc := range cases {
		route, pathEnding, ok := signUpLandingShellMapping(tc.requestPath)
		if ok != tc.wantOK {
			t.Fatalf("signUpLandingShellMapping(%q) ok = %v, want %v", tc.requestPath, ok, tc.wantOK)
		}
		if !tc.wantOK {
			continue
		}
		if route != tc.wantRoute || pathEnding != tc.wantPathEnding {
			t.Fatalf(
				"signUpLandingShellMapping(%q) = (%q, %q), want (%q, %q)",
				tc.requestPath, route, pathEnding, tc.wantRoute, tc.wantPathEnding,
			)
		}
	}
}

// An integration-contributed /sign-up page must never be masked by the landing
// shell fallback.
func TestServeSignUpLandingShellSkippedWhenIntegrationContributesRoute(t *testing.T) {
	contributed := map[string]struct{}{signUpLandingShellPath: {}}
	request := httptest.NewRequest(http.MethodGet, "/sign-up", nil)
	recorder := httptest.NewRecorder()

	if served := serveSignUpLandingShell(contributed, recorder, request); served {
		t.Fatalf("serveSignUpLandingShell served the landing shell although an integration contributes /sign-up")
	}
}

// Non-/sign-up paths stay with the regular fallback chain.
func TestServeSignUpLandingShellIgnoresOtherPaths(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	recorder := httptest.NewRecorder()

	if served := serveSignUpLandingShell(map[string]struct{}{}, recorder, request); served {
		t.Fatalf("serveSignUpLandingShell served the landing shell for /login")
	}
}
