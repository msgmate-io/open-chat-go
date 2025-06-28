package server

import (
	"backend/database"
	"net/http"
	"strings"
)

// browserTokenRoute describes a route that restricted browser-audience
// tokens are allowed to call. Browser tokens are default-deny: any route not
// listed here rejects them, regardless of the scopes they carry.
//
// Patterns are relative to the /api/v1 mount (as seen by handlers after
// http.StripPrefix), with {placeholder} segments matching any single path
// segment.
type browserTokenRoute struct {
	Method  string
	Pattern string
	Scope   string // required scope; empty means any valid browser token may call it
}

var browserTokenRoutes = []browserTokenRoute{
	{Method: http.MethodGet, Pattern: "/user/self", Scope: ""},

	{Method: http.MethodGet, Pattern: "/bots/list", Scope: database.ScopeBotsRead},
	{Method: http.MethodGet, Pattern: "/bots/{identifier}", Scope: database.ScopeBotsRead},
	{Method: http.MethodPatch, Pattern: "/bots/{identifier}", Scope: database.ScopeBotsWrite},
	{Method: http.MethodPut, Pattern: "/bots/{identifier}/config", Scope: database.ScopeBotsWrite},

	{Method: http.MethodGet, Pattern: "/chats/list", Scope: database.ScopeInteractionsList},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}", Scope: database.ScopeInteractionsRead},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}/messages/list", Scope: database.ScopeInteractionsRead},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}/status", Scope: database.ScopeInteractionsRead},
}

func normalizeBrowserRoutePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	// Handlers behind the /api/v1 mount see stripped paths; routes registered
	// on the root mux keep the full prefix. Normalize both to the v1-relative
	// form used by browserTokenRoutes.
	if strings.HasPrefix(trimmed, "/api/v1/") || trimmed == "/api/v1" {
		trimmed = strings.TrimPrefix(trimmed, "/api/v1")
		if trimmed == "" {
			trimmed = "/"
		}
	}
	return trimmed
}

func browserRoutePatternMatches(pattern, path string) bool {
	patternSegments := strings.Split(strings.Trim(pattern, "/"), "/")
	pathSegments := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternSegments) != len(pathSegments) {
		return false
	}
	for i, segment := range patternSegments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			continue
		}
		if segment != pathSegments[i] {
			return false
		}
	}
	return true
}

// matchBrowserTokenRoute resolves the allowlisted route for a restricted
// token request. ok is false when the route is not allowlisted (default
// deny). The returned scope is the scope the token must carry; empty means
// no specific scope is required.
func matchBrowserTokenRoute(method, path string) (scope string, ok bool) {
	normalized := normalizeBrowserRoutePath(path)
	for _, route := range browserTokenRoutes {
		if route.Method != method {
			continue
		}
		if browserRoutePatternMatches(route.Pattern, normalized) {
			return route.Scope, true
		}
	}
	return "", false
}

// CORSMiddleware serves cross-origin requests for an explicit origin
// allowlist. It never allows credentials (browser clients authenticate with
// Bearer tokens, never cookies) and rejects wildcard origins at config parse
// time. Preflight OPTIONS requests are answered directly.
func CORSMiddleware(allowedOrigins []string) Middleware {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" || len(allowed) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			_, originAllowed := allowed[strings.ToLower(strings.TrimRight(origin, "/"))]
			if r.Method == http.MethodOptions {
				if !originAllowed {
					http.Error(w, "Origin not allowed", http.StatusForbidden)
					return
				}
				headers := w.Header()
				headers.Set("Access-Control-Allow-Origin", origin)
				headers.Set("Vary", "Origin")
				headers.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				headers.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Origin, X-Requested-With")
				headers.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			if originAllowed {
				headers := w.Header()
				headers.Set("Access-Control-Allow-Origin", origin)
				headers.Set("Vary", "Origin")
			}
			next.ServeHTTP(w, r)
		})
	}
}
