package server

import (
	"backend/database"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

func setupMiddlewareTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "middleware_browser_tokens_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createMiddlewareTestUser(t *testing.T, DB *gorm.DB, name string) *database.User {
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

func createAccessToken(t *testing.T, DB *gorm.DB, user *database.User, mutate func(token *database.AccessToken)) (*database.AccessToken, string) {
	t.Helper()
	raw, prefix, hash, err := database.GenerateRawAccessToken()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	token := &database.AccessToken{
		UserId:      user.ID,
		Name:        "test token",
		TokenPrefix: prefix,
		TokenHash:   hash,
	}
	if mutate != nil {
		mutate(token)
	}
	if err := DB.Create(token).Error; err != nil {
		t.Fatalf("failed to create access token: %v", err)
	}
	return token, raw
}

func bearerRequest(method, target, rawToken string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if rawToken != "" {
		req.Header.Set("Authorization", "Bearer "+rawToken)
	}
	return req
}

func TestResolveBearerTokenGeneralUnchanged(t *testing.T) {
	DB := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, DB, "general-user")
	_, raw := createAccessToken(t, DB, user, nil)

	// General tokens keep working on any authenticated route.
	resolvedUser, token, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/contacts/list", raw))
	if !ok || resolvedUser == nil || token == nil {
		t.Fatalf("expected general token to resolve on arbitrary route")
	}
	if resolvedUser.ID != user.ID {
		t.Fatalf("expected resolved user %d, got %d", user.ID, resolvedUser.ID)
	}
}

func TestResolveBrowserTokenDefaultDeny(t *testing.T) {
	DB := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, DB, "browser-user")
	_, raw := createAccessToken(t, DB, user, func(token *database.AccessToken) {
		token.Audience = database.AudienceBrowserAPI
		token.Scopes = database.JoinTokenScopes([]string{
			database.ScopeBotsRead, database.ScopeBotsWrite,
			database.ScopeInteractionsList, database.ScopeInteractionsRead,
		})
	})

	denied := []struct{ method, target string }{
		{"GET", "/contacts/list"},
		{"POST", "/bots"},
		{"DELETE", "/bots/some-uuid"},
		{"POST", "/bots/some-uuid/interactions"},
		{"POST", "/chats/create"},
		{"POST", "/chats/some-uuid/messages/send"},
		{"POST", "/user/access-tokens"},
		{"POST", "/user/browser-token"},
		{"GET", "/user/permissions"},
		{"GET", "/admin/users"},
		{"GET", "/api/v1/models"},
		{"GET", "/connect"},
	}
	for _, route := range denied {
		if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest(route.method, route.target, raw)); ok {
			t.Errorf("expected browser token to be denied on %s %s", route.method, route.target)
		}
	}

	allowed := []struct{ method, target string }{
		{"GET", "/bots/list"},
		{"GET", "/api/v1/bots/list"},
		{"GET", "/bots/some-uuid"},
		{"PATCH", "/bots/some-uuid"},
		{"PUT", "/bots/some-uuid/config"},
		{"GET", "/chats/list"},
		{"GET", "/chats/some-uuid"},
		{"GET", "/chats/some-uuid/messages/list"},
		{"GET", "/chats/some-uuid/status"},
		{"GET", "/user/self"},
	}
	for _, route := range allowed {
		if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest(route.method, route.target, raw)); !ok {
			t.Errorf("expected browser token to resolve on %s %s", route.method, route.target)
		}
	}
}

func TestResolveBrowserTokenScopeEnforcement(t *testing.T) {
	DB := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, DB, "scoped-user")
	_, raw := createAccessToken(t, DB, user, func(token *database.AccessToken) {
		token.Audience = database.AudienceBrowserAPI
		token.Scopes = database.ScopeBotsRead
	})

	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", raw)); !ok {
		t.Fatalf("expected bots:read token to resolve on GET /bots/list")
	}
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("PATCH", "/bots/some-uuid", raw)); ok {
		t.Fatalf("expected bots:read token to be rejected on PATCH /bots/{identifier} (missing bots:write)")
	}
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/chats/list", raw)); ok {
		t.Fatalf("expected bots:read token to be rejected on GET /chats/list (missing interactions:list)")
	}
}

func TestResolveBrowserTokenExpiryAndRevocation(t *testing.T) {
	DB := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, DB, "expiry-user")

	past := time.Now().Add(-time.Minute)
	_, expiredRaw := createAccessToken(t, DB, user, func(token *database.AccessToken) {
		token.Audience = database.AudienceBrowserAPI
		token.Scopes = database.ScopeBotsRead
		token.ExpiresAt = &past
	})
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", expiredRaw)); ok {
		t.Fatalf("expected expired browser token to be rejected")
	}

	_, revokedRaw := createAccessToken(t, DB, user, func(token *database.AccessToken) {
		token.Audience = database.AudienceBrowserAPI
		token.Scopes = database.ScopeBotsRead
		now := time.Now()
		token.RevokedAt = &now
	})
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", revokedRaw)); ok {
		t.Fatalf("expected revoked browser token to be rejected")
	}
}

func TestResolveBrowserTokenParentInvalidation(t *testing.T) {
	DB := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, DB, "parent-user")

	parent, _ := createAccessToken(t, DB, user, nil)
	child := func(mutate func(token *database.AccessToken)) string {
		_, childRaw := createAccessToken(t, DB, user, func(token *database.AccessToken) {
			token.Audience = database.AudienceBrowserAPI
			token.Scopes = database.ScopeBotsRead
			token.ParentTokenId = &parent.ID
			if mutate != nil {
				mutate(token)
			}
		})
		return childRaw
	}

	childRaw := child(nil)
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", childRaw)); !ok {
		t.Fatalf("expected child token with valid parent to resolve")
	}

	// Parent revocation immediately invalidates the child.
	now := time.Now()
	if err := DB.Model(parent).Update("revoked_at", &now).Error; err != nil {
		t.Fatalf("failed to revoke parent: %v", err)
	}
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", childRaw)); ok {
		t.Fatalf("expected child token to be rejected after parent revocation")
	}

	// Parent expiry immediately invalidates the child.
	future := time.Now().Add(time.Hour)
	if err := DB.Model(parent).Updates(map[string]interface{}{"revoked_at": nil, "expires_at": &future}).Error; err != nil {
		t.Fatalf("failed to restore parent: %v", err)
	}
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", childRaw)); !ok {
		t.Fatalf("expected child token to resolve with valid parent again")
	}
	past := time.Now().Add(-time.Minute)
	if err := DB.Model(parent).Update("expires_at", &past).Error; err != nil {
		t.Fatalf("failed to expire parent: %v", err)
	}
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", childRaw)); ok {
		t.Fatalf("expected child token to be rejected after parent expiry")
	}

	// Missing parent invalidates the child.
	orphanRaw := child(func(token *database.AccessToken) {
		missing := uint(999999)
		token.ParentTokenId = &missing
	})
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", orphanRaw)); ok {
		t.Fatalf("expected child token with missing parent to be rejected")
	}
}

func TestRevokeChildAccessTokensCascade(t *testing.T) {
	DB := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, DB, "cascade-user")

	parent, raw := createAccessToken(t, DB, user, nil)
	_, childRaw := createAccessToken(t, DB, user, func(token *database.AccessToken) {
		token.Audience = database.AudienceBrowserAPI
		token.Scopes = database.ScopeBotsRead
		token.ParentTokenId = &parent.ID
	})

	if err := database.RevokeChildAccessTokens(DB, parent.ID); err != nil {
		t.Fatalf("failed to revoke children: %v", err)
	}

	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/bots/list", childRaw)); ok {
		t.Fatalf("expected child token to be rejected after cascade revocation")
	}
	if _, _, ok := resolveUserFromBearerToken(DB, bearerRequest("GET", "/contacts/list", raw)); !ok {
		t.Fatalf("expected parent token to remain valid after child cascade")
	}
}
