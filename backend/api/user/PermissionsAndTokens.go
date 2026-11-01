package user

import (
	"backend/database"
	"backend/server/util"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

const maxUserAccessTokens = 5
const cliBrowserAuthStateTTL = 5 * time.Minute

// cliBrowserAuthTokenTTL bounds the lifetime of the CLI credential minted by
// the browser flow. The flow is a convenience for interactive use, so a
// short-lived token limits the blast radius if a minted credential is ever
// exposed (see ci#936).
const cliBrowserAuthTokenTTL = 15 * time.Minute

// cliBrowserAuthResult is the outcome of a completed CLI browser
// authorization stored under the server-generated state. The poller must
// present the paired secret (cliCodeVerifier) to collect it: the state alone
// is not a credential (see ci#936).
type cliBrowserAuthResult struct {
	Token        string
	Error        string
	ExpiresAt    time.Time
	CodeVerifier string
}

var cliBrowserAuthResults = struct {
	mu    sync.Mutex
	state map[string]cliBrowserAuthResult
}{
	state: map[string]cliBrowserAuthResult{},
}

type PermissionsResponse struct {
	Rows []string `json:"rows"`
}

type CreateAccessTokenRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	BotUUID   *string `json:"bot_uuid,omitempty"`
}

type AccessTokenListItem struct {
	UUID        string     `json:"uuid"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Scope       string     `json:"scope"`
	BotUUID     *string    `json:"bot_uuid,omitempty"`
	TokenPrefix string     `json:"token_prefix"`
	Audience    string     `json:"audience,omitempty"`
	Scopes      string     `json:"scopes,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type AccessTokenCreateResponse struct {
	Success     bool                `json:"success"`
	Token       string              `json:"token"`
	AccessToken AccessTokenListItem `json:"access_token"`
}

type AccessTokensListResponse struct {
	database.Pagination
	Rows []AccessTokenListItem `json:"rows"`
}

const botScopedTokenNamePrefix = "bot:"

func composeTokenName(displayName string, botUUID *string) string {
	trimmed := strings.TrimSpace(displayName)
	if botUUID == nil || strings.TrimSpace(*botUUID) == "" {
		return trimmed
	}
	return fmt.Sprintf("%s%s|%s", botScopedTokenNamePrefix, strings.TrimSpace(*botUUID), trimmed)
}

func parseTokenName(rawName string) (displayName string, scope string, botUUID *string) {
	name := strings.TrimSpace(rawName)
	if !strings.HasPrefix(name, botScopedTokenNamePrefix) {
		return name, "account", nil
	}
	payload := strings.TrimPrefix(name, botScopedTokenNamePrefix)
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return name, "account", nil
	}
	trimmedBotUUID := strings.TrimSpace(parts[0])
	if trimmedBotUUID == "" {
		return name, "account", nil
	}
	botUUIDCopy := trimmedBotUUID
	return strings.TrimSpace(parts[1]), "bot", &botUUIDCopy
}

func resolveOwnedBotByUUID(DB *gorm.DB, user *database.User, botUUID string) (database.BotRuntimeConfig, error) {
	if strings.TrimSpace(botUUID) == "" {
		return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
	}

	query := DB.Where("uuid = ?", botUUID)
	if !user.IsAdmin {
		ownedRuntimeIDs := DB.Model(&database.BotRuntimeOwner{}).
			Select("bot_runtime_config_id").
			Where("user_id = ?", user.ID)
		query = query.Where("id IN (?)", ownedRuntimeIDs)
	}

	var runtime database.BotRuntimeConfig
	if err := query.First(&runtime).Error; err != nil {
		return database.BotRuntimeConfig{}, err
	}
	return runtime, nil
}

func hasPermission(DB *gorm.DB, user *database.User, permission database.PermissionName) bool {
	if user.IsAdmin {
		return true
	}
	var userPermission database.Permission
	q := DB.First(&userPermission, "user_id = ? AND permission = ?", user.ID, permission)
	return q.Error == nil
}

func requirePermission(DB *gorm.DB, user *database.User, permission database.PermissionName) bool {
	return hasPermission(DB, user, permission)
}

func (h *UserHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	permissionsSet := map[string]struct{}{}
	if user.IsAdmin {
		permissionsSet[string(database.PermissionCreateAPITokens)] = struct{}{}
		permissionsSet[string(database.PermissionCreateBots)] = struct{}{}
	}

	var rows []database.Permission
	if err := DB.Where("user_id = ?", user.ID).Find(&rows).Error; err == nil {
		for _, row := range rows {
			permissionsSet[string(row.Permission)] = struct{}{}
		}
	}

	permissions := make([]string, 0, len(permissionsSet))
	for permission := range permissionsSet {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PermissionsResponse{Rows: permissions})
}

func (h *UserHandler) CreateAccessToken(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !requirePermission(DB, user, database.PermissionCreateAPITokens) {
		http.Error(w, "Missing permission: create_api_tokens", http.StatusForbidden)
		return
	}

	var req CreateAccessTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "Token name is required", http.StatusBadRequest)
		return
	}
	trimmedName := strings.TrimSpace(req.Name)
	if trimmedName == "" {
		http.Error(w, "Token name is required", http.StatusBadRequest)
		return
	}

	var botUUID *string
	if req.BotUUID != nil {
		trimmedBotUUID := strings.TrimSpace(*req.BotUUID)
		if trimmedBotUUID != "" {
			if _, err := resolveOwnedBotByUUID(DB, user, trimmedBotUUID); err != nil {
				if err == gorm.ErrRecordNotFound {
					http.Error(w, "Bot not found", http.StatusNotFound)
					return
				}
				http.Error(w, "Failed to resolve bot", http.StatusInternalServerError)
				return
			}
			botUUID = &trimmedBotUUID
		}
	}

	if !user.IsAdmin {
		var activeCount int64
		now := time.Now()
		// Restricted audience tokens (e.g. short-lived browser session
		// tokens) rotate automatically and are not counted against the
		// regular API token quota.
		DB.Model(&database.AccessToken{}).
			Where("user_id = ? AND audience = '' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", user.ID, now).
			Count(&activeCount)
		if activeCount >= maxUserAccessTokens {
			http.Error(w, fmt.Sprintf("Token limit reached (max %d for regular users)", maxUserAccessTokens), http.StatusConflict)
			return
		}
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			http.Error(w, "Invalid expires_at format (RFC3339 required)", http.StatusBadRequest)
			return
		}
		expiresAt = &parsed
	}

	rawToken, prefix, tokenHash, err := database.GenerateRawAccessToken()
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	accessToken := database.AccessToken{
		UserId:      user.ID,
		Name:        composeTokenName(trimmedName, botUUID),
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
		ExpiresAt:   expiresAt,
	}
	if err := DB.Create(&accessToken).Error; err != nil {
		http.Error(w, "Failed to persist token", http.StatusInternalServerError)
		return
	}

	displayName, scope, parsedBotUUID := parseTokenName(accessToken.Name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AccessTokenCreateResponse{
		Success: true,
		Token:   rawToken,
		AccessToken: AccessTokenListItem{
			UUID:        accessToken.UUID,
			Name:        accessToken.Name,
			DisplayName: displayName,
			Scope:       scope,
			BotUUID:     parsedBotUUID,
			TokenPrefix: accessToken.TokenPrefix,
			Audience:    accessToken.Audience,
			Scopes:      accessToken.Scopes,
			CreatedAt:   accessToken.CreatedAt,
			LastUsedAt:  accessToken.LastUsedAt,
			ExpiresAt:   accessToken.ExpiresAt,
			RevokedAt:   accessToken.RevokedAt,
		},
	})
}

// tokenExpiryPtr returns a pointer to the expiry time offset from now by ttl.
func tokenExpiryPtr(ttl time.Duration) *time.Time {
	expiresAt := time.Now().Add(ttl)
	return &expiresAt
}

func (h *UserHandler) CLIBrowserAuthStart(w http.ResponseWriter, r *http.Request) {
	// The poller must present the paired secret (code_verifier) when it
	// collects the result: the state alone is not a credential. Issuing a
	// fresh server-generated state per request ensures an attacker cannot
	// pre-seed a state they know and later collect the token minted under it
	// (see ci#936). Legitimate clients complete the flow in seconds, so a
	// short TTL keeps the in-memory store bounded.
	state, err := randomURLSafeToken(24)
	if err != nil {
		http.Error(w, "Failed to generate auth state", http.StatusInternalServerError)
		return
	}
	// The paired secret is generated server-side together with the state and
	// kept out of the URL: the authorize page must show it to the user (for
	// manual copy) or the client passes it to the authorize URL, and the poll
	// step requires it again.
	codeVerifier, err := randomURLSafeToken(24)
	if err != nil {
		http.Error(w, "Failed to generate auth secret", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	cliBrowserAuthResults.mu.Lock()
	cleanupExpiredCLIAuthResultsLocked(now)
	cliBrowserAuthResults.state[state] = cliBrowserAuthResult{
		CodeVerifier: codeVerifier,
		ExpiresAt:    now.Add(cliBrowserAuthStateTTL),
	}
	cliBrowserAuthResults.mu.Unlock()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"state": state, "code_verifier": codeVerifier})
}

func (h *UserHandler) CLIBrowserAuth(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusInternalServerError)
		return
	}

	redirectURI := strings.TrimSpace(r.URL.Query().Get("redirect_uri"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	codeVerifier := strings.TrimSpace(r.URL.Query().Get("code_verifier"))
	tokenName := strings.TrimSpace(r.URL.Query().Get("name"))
	if tokenName == "" {
		tokenName = "open-chat-cli"
	}

	if state == "" {
		http.Error(w, "state is required", http.StatusBadRequest)
		return
	}
	if !isValidCLIAuthState(state) {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	// The state must have been issued by CLIBrowserAuthStart and must not
	// already hold a result: minting under a caller-supplied state is the
	// exact hijack primitive this flow previously exposed (see ci#936).
	// The paired secret proves the caller started the flow.
	if !isPendingCLIAuthState(state, codeVerifier) {
		http.Error(w, "state was not issued by this server", http.StatusBadRequest)
		return
	}
	if redirectURI != "" && !isValidRedirectURL(redirectURI) {
		http.Error(w, "redirect_uri must target localhost loopback", http.StatusBadRequest)
		return
	}

	user, ok := resolveUserFromSessionCookie(DB, r)
	if !ok {
		currentURL := requestAbsoluteURL(r)
		http.Redirect(w, r, loginRedirectURL(currentURL), http.StatusFound)
		return
	}

	if !requirePermission(DB, user, database.PermissionCreateAPITokens) {
		handleCLIAuthResult(w, r, redirectURI, state, "Missing permission: create_api_tokens", "")
		return
	}

	rawToken, prefix, tokenHash, genErr := database.GenerateRawAccessToken()
	if genErr != nil {
		handleCLIAuthResult(w, r, redirectURI, state, "Failed to generate token", "")
		return
	}

	accessToken := database.AccessToken{
		UserId:      user.ID,
		Name:        composeTokenName(tokenName, nil),
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
		ExpiresAt:   tokenExpiryPtr(cliBrowserAuthTokenTTL),
	}
	if createErr := DB.Create(&accessToken).Error; createErr != nil {
		handleCLIAuthResult(w, r, redirectURI, state, "Failed to persist token", "")
		return
	}

	handleCLIAuthResult(w, r, redirectURI, state, "", rawToken)
}

func (h *UserHandler) CLIBrowserAuthPoll(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	codeVerifier := strings.TrimSpace(r.URL.Query().Get("code_verifier"))
	if state == "" {
		http.Error(w, "state is required", http.StatusBadRequest)
		return
	}
	if codeVerifier == "" {
		http.Error(w, "code_verifier is required", http.StatusBadRequest)
		return
	}
	if !isValidCLIAuthState(state) {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	result, found := popCLIAuthResult(state, codeVerifier)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if !found {
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": false})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ready": true,
		"token": result.Token,
		"error": result.Error,
	})
}

func resolveUserFromSessionCookie(DB *gorm.DB, r *http.Request) (*database.User, bool) {
	if DB == nil || r == nil {
		return nil, false
	}
	sessionCookie, err := r.Cookie("session_id")
	if err != nil || strings.TrimSpace(sessionCookie.Value) == "" {
		return nil, false
	}

	var session database.Session
	now := time.Now()
	if err := DB.Where("token = ? AND expiry > ?", strings.TrimSpace(sessionCookie.Value), now).First(&session).Error; err != nil {
		return nil, false
	}

	var user database.User
	if err := DB.First(&user, "id = ?", session.UserId).Error; err != nil {
		return nil, false
	}
	return &user, true
}

func requestAbsoluteURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host + r.URL.RequestURI()
}

func isValidRedirectURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	hostname := strings.TrimSpace(parsed.Hostname())
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func isValidCLIAuthState(state string) bool {
	if len(state) < 16 || len(state) > 200 {
		return false
	}
	for _, ch := range state {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			continue
		}
		if ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}

// isPendingCLIAuthState reports whether state was issued by
// CLIBrowserAuthStart and no result has been stored under it yet. Minting a
// token is only allowed for a pending server-issued state, and only when the
// caller proves it started the flow by presenting the paired secret.
func isPendingCLIAuthState(state, codeVerifier string) bool {
	now := time.Now()
	cliBrowserAuthResults.mu.Lock()
	defer cliBrowserAuthResults.mu.Unlock()

	cleanupExpiredCLIAuthResultsLocked(now)
	result, found := cliBrowserAuthResults.state[state]
	if !found || result.Token != "" || result.Error != "" {
		return false
	}
	return constantTimeEqual(result.CodeVerifier, codeVerifier)
}

// constantTimeEqual compares two strings without leaking their length
// difference through early return.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func loginRedirectURL(target string) string {
	escaped := url.QueryEscape(strings.TrimSpace(target))
	return "/login?redirect=" + escaped + "&next=" + escaped
}

func handleCLIAuthResult(w http.ResponseWriter, r *http.Request, redirectURI, state, authErr, token string) {
	if strings.TrimSpace(redirectURI) != "" {
		redirectToCLIAuthCallback(w, r, redirectURI, state, authErr, token)
		return
	}
	setCLIAuthResult(state, token, authErr)
	if authErr != "" {
		writeCLIAuthResultPage(w, false, authErr)
		return
	}
	writeCLIAuthResultPage(w, true, "Open Chat CLI authenticated. You can close this tab and return to your terminal.")
}

func writeCLIAuthResultPage(w http.ResponseWriter, ok bool, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	status := http.StatusOK
	if !ok {
		status = http.StatusForbidden
	}
	w.WriteHeader(status)
	title := "Open Chat CLI auth complete"
	if !ok {
		title = "Open Chat CLI auth failed"
	}
	_, _ = w.Write([]byte("<html><body><h2>" + title + "</h2><p>" + message + "</p></body></html>"))
}

func setCLIAuthResult(state, token, authErr string) {
	now := time.Now()
	cliBrowserAuthResults.mu.Lock()
	defer cliBrowserAuthResults.mu.Unlock()

	cleanupExpiredCLIAuthResultsLocked(now)
	// Preserve the paired secret issued at start: the poller still needs it to
	// collect the result.
	codeVerifier := cliBrowserAuthResults.state[state].CodeVerifier
	cliBrowserAuthResults.state[state] = cliBrowserAuthResult{
		Token:        strings.TrimSpace(token),
		Error:        strings.TrimSpace(authErr),
		ExpiresAt:    now.Add(cliBrowserAuthStateTTL),
		CodeVerifier: codeVerifier,
	}
}

// popCLIAuthResult atomically removes and returns the result stored under
// state, but only when the caller presents the paired secret. Presenting the
// wrong (or no) secret must not consume the result, otherwise an attacker who
// can guess a state could destroy a legitimate CLI handshake.
func popCLIAuthResult(state, codeVerifier string) (cliBrowserAuthResult, bool) {
	now := time.Now()
	cliBrowserAuthResults.mu.Lock()
	defer cliBrowserAuthResults.mu.Unlock()

	cleanupExpiredCLIAuthResultsLocked(now)
	result, found := cliBrowserAuthResults.state[state]
	if !found {
		return cliBrowserAuthResult{}, false
	}
	if result.CodeVerifier == "" || !constantTimeEqual(result.CodeVerifier, codeVerifier) {
		return cliBrowserAuthResult{}, false
	}
	delete(cliBrowserAuthResults.state, state)
	return result, true
}

func cleanupExpiredCLIAuthResultsLocked(now time.Time) {
	for key, value := range cliBrowserAuthResults.state {
		if now.After(value.ExpiresAt) {
			delete(cliBrowserAuthResults.state, key)
		}
	}
}

func redirectToCLIAuthCallback(w http.ResponseWriter, r *http.Request, redirectURI, state, authErr, token string) {
	if !isValidRedirectURL(redirectURI) {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := parsed.Query()
	q.Set("state", state)
	if token != "" {
		q.Set("token", token)
	}
	if authErr != "" {
		q.Set("error", authErr)
	}
	parsed.RawQuery = q.Encode()
	http.Redirect(w, r, parsed.String(), http.StatusFound)
}

func (h *UserHandler) ListAccessTokens(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !requirePermission(DB, user, database.PermissionCreateAPITokens) {
		http.Error(w, "Missing permission: create_api_tokens", http.StatusForbidden)
		return
	}

	botUUIDFilter := strings.TrimSpace(r.URL.Query().Get("bot_uuid"))
	if botUUIDFilter != "" {
		if _, err := resolveOwnedBotByUUID(DB, user, botUUIDFilter); err != nil {
			if err == gorm.ErrRecordNotFound {
				http.Error(w, "Bot not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to resolve bot", http.StatusInternalServerError)
			return
		}
	}

	pagination := database.Pagination{Page: 1, Limit: 20}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}
	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	var totalRows int64
	countQuery := DB.Model(&database.AccessToken{}).Where("user_id = ?", user.ID)
	if botUUIDFilter != "" {
		countQuery = countQuery.Where("name LIKE ?", fmt.Sprintf("%s%s|%%", botScopedTokenNamePrefix, botUUIDFilter))
	}
	if err := countQuery.Count(&totalRows).Error; err != nil {
		http.Error(w, "Failed to count access tokens", http.StatusInternalServerError)
		return
	}
	pagination.TotalRows = totalRows
	if pagination.Limit > 0 {
		pagination.TotalPages = int((totalRows + int64(pagination.Limit) - 1) / int64(pagination.Limit))
	}

	var rows []database.AccessToken
	listQuery := DB.Where("user_id = ?", user.ID)
	if botUUIDFilter != "" {
		listQuery = listQuery.Where("name LIKE ?", fmt.Sprintf("%s%s|%%", botScopedTokenNamePrefix, botUUIDFilter))
	}
	if err := listQuery.
		Offset(pagination.GetOffset()).
		Limit(pagination.GetLimit()).
		Order(pagination.GetSort()).
		Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list access tokens", http.StatusInternalServerError)
		return
	}

	items := make([]AccessTokenListItem, 0, len(rows))
	for _, token := range rows {
		displayName, scope, parsedBotUUID := parseTokenName(token.Name)
		items = append(items, AccessTokenListItem{
			UUID:        token.UUID,
			Name:        token.Name,
			DisplayName: displayName,
			Scope:       scope,
			BotUUID:     parsedBotUUID,
			TokenPrefix: token.TokenPrefix,
			Audience:    token.Audience,
			Scopes:      token.Scopes,
			CreatedAt:   token.CreatedAt,
			LastUsedAt:  token.LastUsedAt,
			ExpiresAt:   token.ExpiresAt,
			RevokedAt:   token.RevokedAt,
		})
	}

	pagination.Rows = items
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}

func (h *UserHandler) RevokeAccessToken(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !requirePermission(DB, user, database.PermissionCreateAPITokens) {
		http.Error(w, "Missing permission: create_api_tokens", http.StatusForbidden)
		return
	}

	tokenUUID := strings.TrimSpace(r.PathValue("token_uuid"))
	if tokenUUID == "" {
		http.Error(w, "token_uuid is required", http.StatusBadRequest)
		return
	}

	var token database.AccessToken
	if err := DB.Where("uuid = ? AND user_id = ?", tokenUUID, user.ID).First(&token).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "Access token not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load access token", http.StatusInternalServerError)
		return
	}

	if token.RevokedAt == nil {
		now := time.Now()
		if err := DB.Model(&token).Update("revoked_at", &now).Error; err != nil {
			http.Error(w, "Failed to revoke access token", http.StatusInternalServerError)
			return
		}
		token.RevokedAt = &now
	}

	// Revoking a parent credential immediately revokes tokens derived from it
	// (e.g. exchanged browser tokens).
	if err := database.RevokeChildAccessTokens(DB, token.ID); err != nil {
		http.Error(w, "Failed to revoke derived access tokens", http.StatusInternalServerError)
		return
	}

	displayName, scope, parsedBotUUID := parseTokenName(token.Name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"access_token": AccessTokenListItem{
			UUID:        token.UUID,
			Name:        token.Name,
			DisplayName: displayName,
			Scope:       scope,
			BotUUID:     parsedBotUUID,
			TokenPrefix: token.TokenPrefix,
			Audience:    token.Audience,
			Scopes:      token.Scopes,
			CreatedAt:   token.CreatedAt,
			LastUsedAt:  token.LastUsedAt,
			ExpiresAt:   token.ExpiresAt,
			RevokedAt:   token.RevokedAt,
		},
	})
}
