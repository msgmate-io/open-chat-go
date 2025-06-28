package user

import (
	"backend/database"
	"backend/runtimecfg"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBrowserTokenTTLSeconds    = 900
	defaultBrowserTokenMaxTTLSeconds = 3600
	browserTokenNamePrefix           = "browser:"
	maxBrowserTokenLabelLength       = 80
)

type BrowserTokenExchangeRequest struct {
	Scopes     []string `json:"scopes"`
	TTLSeconds *int     `json:"ttl_seconds,omitempty"`
	Label      *string  `json:"label,omitempty"`
}

type BrowserTokenExchangeResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
	ExpiresIn   int       `json:"expires_in"`
	Scopes      []string  `json:"scopes"`
	APIBaseURL  string    `json:"api_base_url"`
}

func browserTokenTTLConfig() (defaultTTL int, maxTTL int) {
	values := runtimecfg.GetAll()
	defaultTTL = defaultBrowserTokenTTLSeconds
	maxTTL = defaultBrowserTokenMaxTTLSeconds
	if ttl, err := strconv.Atoi(strings.TrimSpace(values["BROWSER_TOKEN_TTL_SECONDS"].Value)); err == nil && ttl > 0 {
		defaultTTL = ttl
	}
	if configuredMax, err := strconv.Atoi(strings.TrimSpace(values["BROWSER_TOKEN_MAX_TTL_SECONDS"].Value)); err == nil && configuredMax > 0 {
		maxTTL = configuredMax
	}
	if defaultTTL > maxTTL {
		defaultTTL = maxTTL
	}
	return defaultTTL, maxTTL
}

// stripDefaultPort removes the default port for a scheme from a host string.
func stripDefaultPort(scheme, host string) string {
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	if (strings.EqualFold(scheme, "http") && port == "80") || (strings.EqualFold(scheme, "https") && port == "443") {
		return hostname
	}
	return host
}

// originMatchesRequestHost reports whether the Origin header belongs to the
// same origin as the request itself. Used as CSRF protection for
// cookie-authenticated requests: cross-site requests always carry a foreign
// Origin on POSTs, so a mismatched (or missing) Origin is rejected.
func originMatchesRequestHost(originHeader string, r *http.Request) bool {
	origin := strings.TrimSpace(originHeader)
	if origin == "" || r == nil {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	requestHost := strings.TrimSpace(r.Host)
	if requestHost == "" {
		return false
	}

	originHost := stripDefaultPort(parsed.Scheme, parsed.Host)
	requestHostNoPort := stripDefaultPort(parsed.Scheme, requestHost)
	if strings.EqualFold(originHost, requestHost) || strings.EqualFold(originHost, requestHostNoPort) {
		return true
	}
	return false
}

func publicAPIBaseURL(r *http.Request) string {
	if configured := runtimecfg.PublicBaseURL(); configured != "" {
		return strings.TrimRight(configured, "/")
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if commaIdx := strings.Index(host, ","); commaIdx >= 0 {
		host = strings.TrimSpace(host[:commaIdx])
	}
	if host == "" {
		return ""
	}

	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + host
}

// ExchangeBrowserToken mints a short-lived, scope-restricted browser token
// for the authenticated user. The caller authenticates either with an
// existing bearer access token (server-to-server integrations) or with a
// session cookie (in which case a same-site Origin header is required as
// CSRF protection).
//
// Browser tokens are default-deny: they only work on explicitly allowlisted
// bot/interaction routes, never exceed the parent credential's authority or
// lifetime, and are invalidated immediately when the parent credential is
// revoked or expires.
//
//	@Summary      Exchange credentials for a short-lived browser token
//	@Description  Exchange the caller's existing credential for a short-lived scoped browser API token
//	@Tags         user
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Security     BearerAuth
//	@Param        request body user.BrowserTokenExchangeRequest true "Requested scopes and optional TTL"
//	@Success      200 {object} user.BrowserTokenExchangeResponse "Ephemeral browser token"
//	@Failure      400 {string} string "Invalid scopes or TTL"
//	@Failure      403 {string} string "Forbidden"
//	@Router       /api/v1/user/browser-token [post]
func (h *UserHandler) ExchangeBrowserToken(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	parentToken := database.AccessTokenFromContext(r.Context())
	if parentToken != nil {
		// Restricted tokens cannot mint further tokens (no privilege or
		// lifetime escalation chains).
		if parentToken.IsRestricted() {
			http.Error(w, "Restricted tokens cannot exchange browser tokens", http.StatusForbidden)
			return
		}
	} else {
		// Session-cookie authenticated: require a same-site Origin header so
		// cross-site pages cannot exchange tokens with a victim's session.
		if !originMatchesRequestHost(r.Header.Get("Origin"), r) {
			http.Error(w, "Origin does not match request host", http.StatusForbidden)
			return
		}
	}

	var req BrowserTokenExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if len(req.Scopes) == 0 {
		http.Error(w, "scopes is required and must not be empty", http.StatusBadRequest)
		return
	}
	requestedScopes := make([]string, 0, len(req.Scopes))
	seen := map[string]struct{}{}
	for _, rawScope := range req.Scopes {
		scope := strings.TrimSpace(rawScope)
		if scope == "" {
			http.Error(w, "scopes must not contain empty values", http.StatusBadRequest)
			return
		}
		if !database.IsValidTokenScope(scope) {
			http.Error(w, fmt.Sprintf("invalid scope: %s", scope), http.StatusBadRequest)
			return
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		requestedScopes = append(requestedScopes, scope)
	}

	// Requested scopes must be a subset of the parent credential's authority.
	if parentToken != nil {
		parentScopes := database.ParseTokenScopes(parentToken.Scopes)
		if !database.ScopesSubset(parentScopes, requestedScopes) {
			http.Error(w, "requested scopes exceed parent token authority", http.StatusForbidden)
			return
		}
	}

	defaultTTL, maxTTL := browserTokenTTLConfig()
	ttlSeconds := defaultTTL
	if req.TTLSeconds != nil {
		ttlSeconds = *req.TTLSeconds
	}
	if ttlSeconds <= 0 || ttlSeconds > maxTTL {
		http.Error(w, fmt.Sprintf("ttl_seconds must be between 1 and %d", maxTTL), http.StatusBadRequest)
		return
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)

	// Child lifetime must never exceed the parent credential's lifetime.
	if parentToken != nil && parentToken.ExpiresAt != nil {
		if !parentToken.ExpiresAt.After(now) {
			http.Error(w, "parent token is expired", http.StatusForbidden)
			return
		}
		if expiresAt.After(*parentToken.ExpiresAt) {
			expiresAt = *parentToken.ExpiresAt
		}
	}

	rawToken, prefix, tokenHash, err := database.GenerateRawAccessToken()
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	name := browserTokenNamePrefix + "browser-api"
	if req.Label != nil {
		label := strings.TrimSpace(*req.Label)
		if len(label) > maxBrowserTokenLabelLength {
			label = label[:maxBrowserTokenLabelLength]
		}
		if label != "" {
			name = name + "|" + label
		}
	}

	accessToken := database.AccessToken{
		UserId:      user.ID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
		ExpiresAt:   &expiresAt,
		Audience:    database.AudienceBrowserAPI,
		Scopes:      database.JoinTokenScopes(requestedScopes),
	}
	if parentToken != nil {
		parentID := parentToken.ID
		accessToken.ParentTokenId = &parentID
	}

	if err := DB.Create(&accessToken).Error; err != nil {
		http.Error(w, "Failed to persist token", http.StatusInternalServerError)
		return
	}

	database.CleanupExpiredBrowserAccessTokens(DB, user.ID)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(BrowserTokenExchangeResponse{
		AccessToken: rawToken,
		TokenType:   "Bearer",
		ExpiresAt:   expiresAt.UTC(),
		ExpiresIn:   int(time.Until(expiresAt).Seconds()),
		Scopes:      requestedScopes,
		APIBaseURL:  publicAPIBaseURL(r),
	})
}
