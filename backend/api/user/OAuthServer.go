package user

import (
	"backend/database"
	"backend/server/util"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// This file implements the MCP OAuth surface described by ci#895 (discovery)
// and ci#900 (authorization server). It is deliberately self-contained and
// mirrors the patterns already used elsewhere in this package:
//
//   - discovery documents (RFC 9728 protected-resource metadata and RFC 8414
//     authorization-server metadata) are served from /.well-known/*;
//   - /register implements RFC 7591 dynamic client registration for public
//     clients (no client secret), pinning the redirect URIs at registration;
//   - /authorize delegates user authentication to the existing OpenChat
//     session-cookie identity layer (resolveUserFromSessionCookie) and issues a
//     single-use authorization code;
//   - /token exchanges that code (authorization_code grant) after verifying
//     PKCE S256 and mints a default-deny `mcp-oauth` bearer through the shared
//     token machinery (database.MintScopedAccessToken), so the serve endpoint's
//     token_hash check can accept it with no change on the serve side.
//
// The registry and the short-lived authorization codes live in memory with a
// TTL, exactly like cliBrowserAuthResults in PermissionsAndTokens.go. Nothing
// here logs, returns or persists a client secret, authorization code or token
// beyond what the caller must receive once.

const (
	// oauthClientTTL is how long a dynamically registered public client stays
	// usable. Registrations are cheap and re-creatable, so a bounded lifetime
	// keeps the in-memory registry from growing without bound.
	oauthClientTTL = 24 * time.Hour
	// oauthAuthorizationCodeTTL is the lifetime of an issued authorization
	// code. RFC 6749 recommends a short maximum (10 minutes); MCP handshakes
	// complete in seconds, so two minutes is already generous.
	oauthAuthorizationCodeTTL = 2 * time.Minute
	// oauthAccessTokenTTL is the lifetime of the minted `mcp-oauth` bearer.
	oauthAccessTokenTTL = time.Hour
)

type oauthClient struct {
	ClientID      string
	ClientName    string
	RedirectURIs  []string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	TokenAuthMode string
}

type oauthAuthorizationCode struct {
	Code          string
	ClientID      string
	UserID        uint
	RedirectURI   string
	CodeChallenge string
	Scope         string
	ExpiresAt     time.Time
}

var oauthClients = struct {
	mu    sync.Mutex
	state map[string]oauthClient
}{
	state: map[string]oauthClient{},
}

var oauthAuthorizationCodes = struct {
	mu    sync.Mutex
	state map[string]oauthAuthorizationCode
}{
	state: map[string]oauthAuthorizationCode{},
}

// oauthPublicBaseURL resolves the absolute public origin used in every
// discovery document and endpoint URL. It reuses the same resolution the
// browser-token exchange uses so self-hosted instances and proxied
// deployments agree on one issuer.
func oauthPublicBaseURL(r *http.Request) string {
	return strings.TrimRight(publicAPIBaseURL(r), "/")
}

// oauthSupportedScopes returns the sorted scopes this authorization server can
// grant. It is derived from the canonical scope set so the two can never drift.
func oauthSupportedScopes() []string {
	scopes := make([]string, 0, len(database.ValidTokenScopes))
	for scope := range database.ValidTokenScopes {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes
}

func randomURLSafeToken(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeOAuthJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	body := map[string]string{"error": code}
	if strings.TrimSpace(description) != "" {
		body["error_description"] = description
	}
	writeOAuthJSON(w, status, body)
}

// ProtectedResourceMetadata serves the RFC 9728 protected-resource metadata for
// the OpenChat MCP resource. An MCP client probes this document to discover the
// authorization server it must talk to (ci#895).
func (h *UserHandler) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := oauthPublicBaseURL(r)
	writeOAuthJSON(w, http.StatusOK, map[string]interface{}{
		"resource":                 base,
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         oauthSupportedScopes(),
		"resource_name":            "OpenChat MCP",
	})
}

// AuthorizationServerMetadata serves the RFC 8414 authorization-server metadata
// advertising the authorize/token/register endpoints and the PKCE S256
// requirement (ci#895, ci#900).
func (h *UserHandler) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := oauthPublicBaseURL(r)
	writeOAuthJSON(w, http.StatusOK, map[string]interface{}{
		"issuer":                                base,
		"authorization_endpoint":                base + "/authorize",
		"token_endpoint":                        base + "/token",
		"registration_endpoint":                 base + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      oauthSupportedScopes(),
	})
}

type oauthRegisterRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// isLoopbackHostname reports whether a hostname is a loopback address
// ("localhost" or a loopback IP).
func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// isOAuthRedirectURI reports whether a redirect URI is an acceptable target for
// a public OAuth client. Loopback HTTP/HTTPS targets (the MCP client's local
// callback listener) are always allowed; other targets must be absolute HTTPS
// URLs. The exact registered value is pinned and matched on /authorize and
// /token, so no open-redirect is possible even for a permissive-looking value.
func isOAuthRedirectURI(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if parsed.Fragment != "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if isLoopbackHostname(parsed.Hostname()) {
			return true
		}
		return strings.EqualFold(parsed.Scheme, "https")
	default:
		// Custom schemes (e.g. a desktop client) are not loopback and are
		// rejected: only header-bearer HTTP(S) callbacks are supported.
		return false
	}
}

// RegisterOAuthClient implements RFC 7591 dynamic client registration for a
// public client. The redirect URIs are pinned here and must be reused verbatim
// on /authorize and /token.
func (h *UserHandler) RegisterOAuthClient(w http.ResponseWriter, r *http.Request) {
	var req oauthRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "request body must be JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	redirectURIs := make([]string, 0, len(req.RedirectURIs))
	for _, raw := range req.RedirectURIs {
		trimmed := strings.TrimSpace(raw)
		if !isOAuthRedirectURI(trimmed) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "unsupported redirect_uri")
			return
		}
		redirectURIs = append(redirectURIs, trimmed)
	}
	for _, grant := range req.GrantTypes {
		if g := strings.TrimSpace(grant); g != "" && g != "authorization_code" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported grant_type")
			return
		}
	}
	if method := strings.TrimSpace(req.TokenEndpointAuthMethod); method != "" && method != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients (token_endpoint_auth_method=none) are supported")
		return
	}

	clientID, err := randomURLSafeToken(24)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate client_id")
		return
	}
	now := time.Now()
	clientName := strings.TrimSpace(req.ClientName)
	if clientName == "" {
		clientName = "mcp-client"
	}

	oauthClients.mu.Lock()
	cleanupExpiredOAuthClientsLocked(now)
	oauthClients.state[clientID] = oauthClient{
		ClientID:      clientID,
		ClientName:    clientName,
		RedirectURIs:  redirectURIs,
		CreatedAt:     now,
		ExpiresAt:     now.Add(oauthClientTTL),
		TokenAuthMode: "none",
	}
	oauthClients.mu.Unlock()

	writeOAuthJSON(w, http.StatusCreated, map[string]interface{}{
		"client_id":                  clientID,
		"client_id_issued_at":        now.Unix(),
		"client_name":                clientName,
		"redirect_uris":              redirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
	})
}

func lookupOAuthClient(clientID string) (oauthClient, bool) {
	now := time.Now()
	oauthClients.mu.Lock()
	defer oauthClients.mu.Unlock()
	cleanupExpiredOAuthClientsLocked(now)
	client, ok := oauthClients.state[strings.TrimSpace(clientID)]
	if !ok || now.After(client.ExpiresAt) {
		return oauthClient{}, false
	}
	return client, true
}

func cleanupExpiredOAuthClientsLocked(now time.Time) {
	for key, client := range oauthClients.state {
		if now.After(client.ExpiresAt) {
			delete(oauthClients.state, key)
		}
	}
}

// isValidRedirectURI reports whether redirectURI exactly matches one of the
// redirect URIs pinned at client registration. Registration only accepts
// absolute HTTP(S) URLs (loopback or HTTPS), so this exact-match allowlist is
// the only redirect target the authorization server will ever emit. The name
// follows the repo-wide isValidRedirectURL convention, which CodeQL also
// recognizes as a redirect sanitizer (go/unvalidated-url-redirection).
func isValidRedirectURI(client oauthClient, redirectURI string) bool {
	for _, candidate := range client.RedirectURIs {
		if candidate == redirectURI {
			return true
		}
	}
	return false
}

// redirectOAuthToClient sends a 302 to the client's registered callback with
// the given response parameters appended. Every redirect emitted by the
// authorization endpoint goes through here and re-checks the exact-match
// allowlist, so a call site can never turn the endpoint into an open redirect
// even if it forgot to validate the parameter first.
func redirectOAuthToClient(w http.ResponseWriter, r *http.Request, client oauthClient, redirectURI string, params url.Values) {
	if redirectURI == "" || !isValidRedirectURI(client, redirectURI) {
		http.Error(w, "redirect_uri is not registered for this client", http.StatusBadRequest)
		return
	}
	parsed, err := url.Parse(redirectURI)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := parsed.Query()
	for key, values := range params {
		for _, value := range values {
			q.Set(key, value)
		}
	}
	parsed.RawQuery = q.Encode()
	http.Redirect(w, r, parsed.String(), http.StatusFound)
}

// redirectOAuthError appends an RFC 6749 error to the client's registered
// redirect URI and returns a 302 to it.
func redirectOAuthError(w http.ResponseWriter, r *http.Request, client oauthClient, redirectURI, state, code, description string) {
	params := url.Values{}
	params.Set("error", code)
	if strings.TrimSpace(description) != "" {
		params.Set("error_description", description)
	}
	if strings.TrimSpace(state) != "" {
		params.Set("state", state)
	}
	redirectOAuthToClient(w, r, client, redirectURI, params)
}

// AuthorizeOAuth implements the authorization endpoint. It validates the
// client and request parameters, authenticates the user through the existing
// session cookie (redirecting to the login screen when absent) and issues a
// single-use authorization code bound to the PKCE challenge.
func (h *UserHandler) AuthorizeOAuth(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusInternalServerError)
		return
	}

	query := r.URL.Query()
	clientID := strings.TrimSpace(query.Get("client_id"))
	redirectURI := strings.TrimSpace(query.Get("redirect_uri"))
	responseType := strings.TrimSpace(query.Get("response_type"))
	state := strings.TrimSpace(query.Get("state"))
	codeChallenge := strings.TrimSpace(query.Get("code_challenge"))
	codeChallengeMethod := strings.TrimSpace(query.Get("code_challenge_method"))
	requestedScope := strings.TrimSpace(query.Get("scope"))

	client, ok := lookupOAuthClient(clientID)
	if !ok {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if redirectURI == "" || !isValidRedirectURI(client, redirectURI) {
		http.Error(w, "redirect_uri is not registered for this client", http.StatusBadRequest)
		return
	}
	// From here the redirect_uri is trusted, so protocol errors are reported
	// back to the client per RFC 6749 §4.1.2.1 instead of as a bare 400.
	if responseType != "code" {
		redirectOAuthError(w, r, client, redirectURI, state, "unsupported_response_type", "only response_type=code is supported")
		return
	}
	if codeChallenge == "" {
		redirectOAuthError(w, r, client, redirectURI, state, "invalid_request", "code_challenge is required (PKCE)")
		return
	}
	if codeChallengeMethod != "" && codeChallengeMethod != "S256" {
		redirectOAuthError(w, r, client, redirectURI, state, "invalid_request", "only code_challenge_method=S256 is supported")
		return
	}
	scope, scopeErr := normalizeOAuthScope(requestedScope)
	if scopeErr != nil {
		redirectOAuthError(w, r, client, redirectURI, state, "invalid_scope", scopeErr.Error())
		return
	}

	user, ok := resolveUserFromSessionCookie(DB, r)
	if !ok {
		currentURL := requestAbsoluteURL(r)
		http.Redirect(w, r, loginRedirectURL(currentURL), http.StatusFound)
		return
	}

	code, genErr := randomURLSafeToken(32)
	if genErr != nil {
		redirectOAuthError(w, r, client, redirectURI, state, "server_error", "failed to issue authorization code")
		return
	}
	now := time.Now()
	oauthAuthorizationCodes.mu.Lock()
	cleanupExpiredOAuthAuthorizationCodesLocked(now)
	oauthAuthorizationCodes.state[code] = oauthAuthorizationCode{
		Code:          code,
		ClientID:      client.ClientID,
		UserID:        user.ID,
		RedirectURI:   redirectURI,
		CodeChallenge: codeChallenge,
		Scope:         scope,
		ExpiresAt:     now.Add(oauthAuthorizationCodeTTL),
	}
	oauthAuthorizationCodes.mu.Unlock()

	params := url.Values{}
	params.Set("code", code)
	if state != "" {
		params.Set("state", state)
	}
	redirectOAuthToClient(w, r, client, redirectURI, params)
}

// normalizeOAuthScope validates a space-delimited scope string against the
// canonical scope set. An empty request yields an empty string (which grants
// the audience's whole allowlist, matching the coder-mcp token behavior).
func normalizeOAuthScope(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	seen := map[string]struct{}{}
	scopes := make([]string, 0)
	for _, part := range strings.Fields(trimmed) {
		if !database.IsValidTokenScope(part) {
			return "", fmt.Errorf("unsupported scope: %s", part)
		}
		if _, dup := seen[part]; dup {
			continue
		}
		seen[part] = struct{}{}
		scopes = append(scopes, part)
	}
	return database.JoinTokenScopes(scopes), nil
}

// ExchangeOAuthToken implements the token endpoint's authorization_code grant
// with PKCE S256 verification. Codes are single-use: a reuse, an expired code,
// or a verifier mismatch all return invalid_grant.
func (h *UserHandler) ExchangeOAuthToken(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "database unavailable")
		return
	}
	if parseErr := r.ParseForm(); parseErr != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	if grantType := strings.TrimSpace(r.Form.Get("grant_type")); grantType != "authorization_code" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "only the authorization_code grant is supported")
		return
	}
	code := strings.TrimSpace(r.Form.Get("code"))
	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	redirectURI := strings.TrimSpace(r.Form.Get("redirect_uri"))
	codeVerifier := strings.TrimSpace(r.Form.Get("code_verifier"))
	if code == "" || clientID == "" || codeVerifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code, client_id and code_verifier are required")
		return
	}

	// A code is single-use: pop it before any validation so a failed attempt
	// can never be retried with the same code.
	authorization, found := popOAuthAuthorizationCode(code)
	if !found {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is unknown, already used or expired")
		return
	}
	if time.Now().After(authorization.ExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code has expired")
		return
	}
	if authorization.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code was issued to a different client")
		return
	}
	if redirectURI != "" && redirectURI != authorization.RedirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization request")
		return
	}
	if !pkceS256Matches(codeVerifier, authorization.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match code_challenge")
		return
	}

	rawToken, _, mintErr := database.MintScopedAccessToken(
		DB,
		authorization.UserID,
		"mcp-oauth",
		database.AudienceMCPOAuth,
		authorization.Scope,
		oauthAccessTokenTTL,
		0,
	)
	if mintErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to mint access token")
		return
	}

	writeOAuthJSON(w, http.StatusOK, map[string]interface{}{
		"access_token": rawToken,
		"token_type":   "Bearer",
		"expires_in":   int(oauthAccessTokenTTL.Seconds()),
		"scope":        strings.ReplaceAll(authorization.Scope, ",", " "),
	})
}

func popOAuthAuthorizationCode(code string) (oauthAuthorizationCode, bool) {
	now := time.Now()
	oauthAuthorizationCodes.mu.Lock()
	defer oauthAuthorizationCodes.mu.Unlock()
	cleanupExpiredOAuthAuthorizationCodesLocked(now)
	authorization, found := oauthAuthorizationCodes.state[strings.TrimSpace(code)]
	if !found {
		return oauthAuthorizationCode{}, false
	}
	delete(oauthAuthorizationCodes.state, code)
	return authorization, true
}

func cleanupExpiredOAuthAuthorizationCodesLocked(now time.Time) {
	for key, authorization := range oauthAuthorizationCodes.state {
		if now.After(authorization.ExpiresAt) {
			delete(oauthAuthorizationCodes.state, key)
		}
	}
}

// pkceS256Matches verifies an RFC 7636 S256 challenge:
// BASE64URL(SHA256(ASCII(code_verifier))) == code_challenge.
func pkceS256Matches(codeVerifier, codeChallenge string) bool {
	if codeVerifier == "" || codeChallenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return computed == codeChallenge
}

// resetOAuthStoresForTest clears the in-memory registries. It exists so tests
// can run hermetically without leaking state between cases.
func resetOAuthStoresForTest() {
	oauthClients.mu.Lock()
	oauthClients.state = map[string]oauthClient{}
	oauthClients.mu.Unlock()
	oauthAuthorizationCodes.mu.Lock()
	oauthAuthorizationCodes.state = map[string]oauthAuthorizationCode{}
	oauthAuthorizationCodes.mu.Unlock()
}
