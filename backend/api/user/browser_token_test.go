package user

import (
	"backend/database"
	"backend/runtimecfg"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func setupBrowserTokenTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "browser_token_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createBrowserTokenTestUser(t *testing.T, DB *gorm.DB, name string) *database.User {
	t.Helper()
	user := database.User{
		Name:     name,
		Username: name,
		Email:    name + "@example.com",
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	return &user
}

func createBrowserTokenTestAccessToken(t *testing.T, DB *gorm.DB, user *database.User, mutate func(token *database.AccessToken)) *database.AccessToken {
	t.Helper()
	_, prefix, hash, err := database.GenerateRawAccessToken()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	token := &database.AccessToken{
		UserId:      user.ID,
		Name:        "parent token",
		TokenPrefix: prefix,
		TokenHash:   hash,
	}
	if mutate != nil {
		mutate(token)
	}
	if err := DB.Create(token).Error; err != nil {
		t.Fatalf("failed to create access token: %v", err)
	}
	return token
}

func exchangeBrowserTokenRequest(t *testing.T, DB *gorm.DB, user *database.User, parent *database.AccessToken, origin string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/user/browser-token", bytes.NewReader(payload))
	req.Host = "open-chat.example"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	if parent != nil {
		ctx = database.ContextWithAccessToken(ctx, parent)
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h := &UserHandler{}
	h.ExchangeBrowserToken(rec, req)
	return rec
}

func decodeExchangeResponse(t *testing.T, rec *httptest.ResponseRecorder) BrowserTokenExchangeResponse {
	t.Helper()
	var response BrowserTokenExchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode exchange response: %v", err)
	}
	return response
}

func TestExchangeBrowserTokenWithBearerParent(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "lw-user")
	parent := createBrowserTokenTestAccessToken(t, DB, user, nil)

	rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
		"scopes": []string{database.ScopeBotsRead, database.ScopeBotsWrite},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}

	response := decodeExchangeResponse(t, rec)
	if !strings.HasPrefix(response.AccessToken, "ocat_") {
		t.Fatalf("expected ocat_ prefixed token, got %q", response.AccessToken)
	}
	if response.TokenType != "Bearer" {
		t.Fatalf("expected token_type Bearer, got %q", response.TokenType)
	}
	if len(response.Scopes) != 2 {
		t.Fatalf("expected 2 scopes in response, got %v", response.Scopes)
	}
	if response.ExpiresIn <= 0 || response.ExpiresIn > defaultBrowserTokenTTLSeconds {
		t.Fatalf("unexpected expires_in %d", response.ExpiresIn)
	}

	var stored database.AccessToken
	if err := DB.Where("token_prefix = ?", response.AccessToken[:18]).First(&stored).Error; err != nil {
		t.Fatalf("failed to load stored token: %v", err)
	}
	if stored.Audience != database.AudienceBrowserAPI {
		t.Fatalf("expected browser-api audience, got %q", stored.Audience)
	}
	if stored.Scopes != database.JoinTokenScopes([]string{database.ScopeBotsRead, database.ScopeBotsWrite}) {
		t.Fatalf("unexpected stored scopes %q", stored.Scopes)
	}
	if stored.ParentTokenId == nil || *stored.ParentTokenId != parent.ID {
		t.Fatalf("expected parent token link %d, got %v", parent.ID, stored.ParentTokenId)
	}
	if stored.ExpiresAt == nil || stored.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expected future expiry on stored token")
	}
}

func TestExchangeBrowserTokenSessionCSRF(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "session-user")
	body := map[string]interface{}{"scopes": []string{database.ScopeBotsRead}}

	t.Run("same origin allowed", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, nil, "https://open-chat.example", body)
		if rec.Code != 200 {
			t.Fatalf("expected 200 for same-origin session exchange, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing origin rejected", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, nil, "", body)
		if rec.Code != 403 {
			t.Fatalf("expected 403 without Origin header, got %d", rec.Code)
		}
	})

	t.Run("cross origin rejected", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, nil, "https://evil.example", body)
		if rec.Code != 403 {
			t.Fatalf("expected 403 for cross-origin session exchange, got %d", rec.Code)
		}
	})

	t.Run("default port equivalence", func(t *testing.T) {
		if !originMatchesRequestHost("https://open-chat.example:443", httptest.NewRequest("POST", "https://open-chat.example/api/v1/user/browser-token", nil)) {
			t.Fatalf("expected https default port origin to match host")
		}
		if originMatchesRequestHost("https://open-chat.example:8443", httptest.NewRequest("POST", "https://open-chat.example/api/v1/user/browser-token", nil)) {
			t.Fatalf("expected non-default port origin to be rejected")
		}
		if originMatchesRequestHost("not-a-url", httptest.NewRequest("POST", "https://open-chat.example/", nil)) {
			t.Fatalf("expected invalid origin to be rejected")
		}
	})
}

func TestExchangeBrowserTokenValidation(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "validation-user")
	parent := createBrowserTokenTestAccessToken(t, DB, user, nil)

	t.Run("empty scopes rejected", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{})
		if rec.Code != 400 {
			t.Fatalf("expected 400 for missing scopes, got %d", rec.Code)
		}
	})

	t.Run("unknown scope rejected", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
			"scopes": []string{"admin:all"},
		})
		if rec.Code != 400 {
			t.Fatalf("expected 400 for unknown scope, got %d", rec.Code)
		}
	})

	t.Run("ttl above max rejected", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
			"scopes":      []string{database.ScopeBotsRead},
			"ttl_seconds": defaultBrowserTokenMaxTTLSeconds + 1,
		})
		if rec.Code != 400 {
			t.Fatalf("expected 400 for excessive ttl, got %d", rec.Code)
		}
	})

	t.Run("zero ttl rejected", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
			"scopes":      []string{database.ScopeBotsRead},
			"ttl_seconds": 0,
		})
		if rec.Code != 400 {
			t.Fatalf("expected 400 for zero ttl, got %d", rec.Code)
		}
	})

	t.Run("restricted parent rejected", func(t *testing.T) {
		restricted := createBrowserTokenTestAccessToken(t, DB, user, func(token *database.AccessToken) {
			token.Audience = database.AudienceBrowserAPI
			token.Scopes = database.ScopeBotsRead
		})
		rec := exchangeBrowserTokenRequest(t, DB, user, restricted, "", map[string]interface{}{
			"scopes": []string{database.ScopeBotsRead},
		})
		if rec.Code != 403 {
			t.Fatalf("expected 403 for restricted parent, got %d", rec.Code)
		}
	})

	t.Run("duplicate scopes deduplicated", func(t *testing.T) {
		rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
			"scopes": []string{database.ScopeBotsRead, database.ScopeBotsRead},
		})
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		response := decodeExchangeResponse(t, rec)
		if len(response.Scopes) != 1 {
			t.Fatalf("expected deduplicated scopes, got %v", response.Scopes)
		}
	})
}

func TestExchangeBrowserTokenChildLifetimeClampedToParent(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "clamp-user")

	parentExpiry := time.Now().Add(2 * time.Minute)
	parent := createBrowserTokenTestAccessToken(t, DB, user, func(token *database.AccessToken) {
		token.ExpiresAt = &parentExpiry
	})

	rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
		"scopes":      []string{database.ScopeBotsRead},
		"ttl_seconds": 3600,
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	response := decodeExchangeResponse(t, rec)
	if response.ExpiresAt.After(parentExpiry.Add(time.Second)) {
		t.Fatalf("child expiry %s must not exceed parent expiry %s", response.ExpiresAt, parentExpiry)
	}

	expiredParent := createBrowserTokenTestAccessToken(t, DB, user, func(token *database.AccessToken) {
		past := time.Now().Add(-time.Minute)
		token.ExpiresAt = &past
	})
	rec = exchangeBrowserTokenRequest(t, DB, user, expiredParent, "", map[string]interface{}{
		"scopes": []string{database.ScopeBotsRead},
	})
	if rec.Code != 403 {
		t.Fatalf("expected 403 for expired parent, got %d", rec.Code)
	}
}

func TestExchangeBrowserTokenTTLConfig(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "ttl-user")
	parent := createBrowserTokenTestAccessToken(t, DB, user, nil)

	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"BROWSER_TOKEN_TTL_SECONDS":     {Value: "60"},
		"BROWSER_TOKEN_MAX_TTL_SECONDS": {Value: "120"},
		"PUBLIC_BASE_URL":               {Value: "https://chat.example.com/"},
	})
	t.Cleanup(func() { runtimecfg.SetAll(map[string]runtimecfg.Value{}) })

	rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
		"scopes": []string{database.ScopeBotsRead},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	response := decodeExchangeResponse(t, rec)
	if response.ExpiresIn > 60 {
		t.Fatalf("expected configured default ttl of 60s, got %d", response.ExpiresIn)
	}
	if response.APIBaseURL != "https://chat.example.com" {
		t.Fatalf("expected api_base_url from PUBLIC_BASE_URL, got %q", response.APIBaseURL)
	}

	rec = exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
		"scopes":      []string{database.ScopeBotsRead},
		"ttl_seconds": 121,
	})
	if rec.Code != 400 {
		t.Fatalf("expected 400 for ttl above configured max, got %d", rec.Code)
	}
}
