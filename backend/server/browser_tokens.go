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

	// Read-only catalogs used by embedded bot editors to populate model,
	// tool, and MCP integration pickers.
	{Method: http.MethodGet, Pattern: "/models", Scope: database.ScopeBotsRead},
	{Method: http.MethodGet, Pattern: "/tools", Scope: database.ScopeBotsRead},
	{Method: http.MethodGet, Pattern: "/integrations/mcp/servers", Scope: database.ScopeBotsRead},

	{Method: http.MethodGet, Pattern: "/chats/list", Scope: database.ScopeInteractionsList},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}", Scope: database.ScopeInteractionsRead},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}/messages/list", Scope: database.ScopeInteractionsRead},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}/status", Scope: database.ScopeInteractionsRead},

	// Read-only interaction history MCP server. Restricted to the dedicated
	// MCP audience/scope the opencode integration mints for agent runtimes.
	{Method: http.MethodPost, Pattern: "/mcp", Scope: database.ScopeInteractionsMCP},
}

// coderTUITokenRoutes describes the routes the short-lived `coder-tui`
// audience token may call. The msgmate_coder integration mints that token for
// the Open-Chat TUI it forwards into a per-chat coding sandbox, so the TUI can
// attach to and chat in the caller's bound session. Unlike the browser
// allowlist it includes the message-send route (and the default-bot lookup the
// TUI uses to fall back to creating a chat); it is still default-deny, so a
// leaked sandbox config can only reach these routes as the minting user.
var coderTUITokenRoutes = []browserTokenRoute{
	{Method: http.MethodGet, Pattern: "/user/self", Scope: ""},
	{Method: http.MethodGet, Pattern: "/chats/list", Scope: ""},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}", Scope: ""},
	{Method: http.MethodGet, Pattern: "/chats/{chat_uuid}/messages/list", Scope: ""},
	{Method: http.MethodPost, Pattern: "/chats/{chat_uuid}/messages/send", Scope: ""},
	// The forwarded TUI surfaces the coder's confirmable actions inline, so it
	// can approve or reject a parked tool call exactly like the web widget.
	{Method: http.MethodPost, Pattern: "/chats/{chat_uuid}/messages/{message_uuid}/confirm-actions/{action_id}/execute", Scope: ""},
	{Method: http.MethodPost, Pattern: "/chats/{chat_uuid}/messages/{message_uuid}/confirm-actions/{action_id}/reject", Scope: ""},
	{Method: http.MethodGet, Pattern: "/contacts/default-bot", Scope: ""},
}

// mcpInteractionTokenRoutes describes the routes the read-only `mcp-interaction`
// audience token may call. The opencode integration mints that token for the
// built-in `open-chat-opencode-mcp` preset, so the sandboxed agent can attach to
// this deployment's own interaction-history MCP server; it must not reach the
// wider owner/admin API surface. It is default-deny apart from the single MCP
// endpoint it needs.
var mcpInteractionTokenRoutes = []browserTokenRoute{
	{Method: http.MethodPost, Pattern: "/mcp", Scope: database.ScopeInteractionsMCP},
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
	return matchTokenRouteList(browserTokenRoutes, method, path)
}

// matchRestrictedTokenRoute dispatches a restricted (audience-bound) token to
// the allowlist for its audience. Unknown audiences are default-deny.
func matchRestrictedTokenRoute(audience, method, path string) (scope string, ok bool) {
	switch strings.TrimSpace(audience) {
	case database.AudienceBrowserAPI:
		return matchTokenRouteList(browserTokenRoutes, method, path)
	case database.AudienceCoderTUI:
		return matchTokenRouteList(coderTUITokenRoutes, method, path)
	case database.AudienceMCPInteraction:
		return matchTokenRouteList(mcpInteractionTokenRoutes, method, path)
	default:
		return "", false
	}
}

// matchTokenRouteList matches a request against one audience's route allowlist.
func matchTokenRouteList(routes []browserTokenRoute, method, path string) (scope string, ok bool) {
	normalized := normalizeBrowserRoutePath(path)
	for _, route := range routes {
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
