package user

import (
	"backend/database"
	"backend/runtimecfg"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func createAccessTokenRequest(t *testing.T, DB *gorm.DB, user *database.User, name string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(CreateAccessTokenRequest{Name: name})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/user/access-tokens", bytes.NewReader(payload))
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h := &UserHandler{}
	h.CreateAccessToken(rec, req)
	return rec
}

func countActiveTokens(t *testing.T, DB *gorm.DB, userID uint, audience string) int64 {
	t.Helper()
	var count int64
	now := time.Now()
	query := DB.Model(&database.AccessToken{}).
		Where("user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", userID, now)
	if audience == "" {
		query = query.Where("audience = ''")
	} else {
		query = query.Where("audience = ?", audience)
	}
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("failed to count tokens: %v", err)
	}
	return count
}

func TestBrowserTokensDoNotCountTowardsRegularTokenLimit(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "limit-user")

	// The user already has one auto-provisioned default API token, so create
	// regular tokens until one slot below the limit remains.
	for i := 0; i < maxUserAccessTokens-2; i++ {
		rec := createAccessTokenRequest(t, DB, user, "regular token")
		if rec.Code != 200 {
			t.Fatalf("expected 200 creating regular token %d, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	expiresAt := time.Now().Add(time.Hour)
	for i := 0; i < maxUserAccessTokens+2; i++ {
		createBrowserTokenTestAccessToken(t, DB, user, func(token *database.AccessToken) {
			token.Name = "browser:browser-api|session"
			token.Audience = database.AudienceBrowserAPI
			token.ExpiresAt = &expiresAt
		})
	}

	rec := createAccessTokenRequest(t, DB, user, "regular token at limit")
	if rec.Code != 200 {
		t.Fatalf("expected browser tokens to not count towards the limit, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = createAccessTokenRequest(t, DB, user, "regular token over limit")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 once the regular token limit is reached, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Token limit reached") {
		t.Fatalf("expected token limit error message, got %q", rec.Body.String())
	}

	if got := countActiveTokens(t, DB, user.ID, ""); got != int64(maxUserAccessTokens) {
		t.Fatalf("expected %d active regular tokens, got %d", maxUserAccessTokens, got)
	}
}

func TestExchangeBrowserTokenRotatesOldestWhenLimitExceeded(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "rotate-user")
	parent := createBrowserTokenTestAccessToken(t, DB, user, nil)

	prev := runtimecfg.GetAll()
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"BROWSER_TOKEN_MAX_ACTIVE_PER_USER": {Value: "3"},
	})
	t.Cleanup(func() { runtimecfg.SetAll(prev) })

	issued := []string{}
	for i := 0; i < 5; i++ {
		rec := exchangeBrowserTokenRequest(t, DB, user, parent, "", map[string]interface{}{
			"scopes": []string{database.ScopeBotsRead},
		})
		if rec.Code != 200 {
			t.Fatalf("expected 200 exchanging browser token %d, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
		response := decodeExchangeResponse(t, rec)
		issued = append(issued, response.AccessToken)
	}

	if got := countActiveTokens(t, DB, user.ID, database.AudienceBrowserAPI); got != 3 {
		t.Fatalf("expected 3 active browser tokens after rotation, got %d", got)
	}

	remaining := []database.AccessToken{}
	if err := DB.Where("user_id = ? AND audience = ?", user.ID, database.AudienceBrowserAPI).
		Order("id ASC").
		Find(&remaining).Error; err != nil {
		t.Fatalf("failed to load remaining browser tokens: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("expected 3 remaining browser token rows, got %d", len(remaining))
	}

	// The newest three issued tokens survive; the two oldest are evicted.
	hashOf := func(raw string) string {
		sum := sha256.Sum256([]byte(raw))
		return hex.EncodeToString(sum[:])
	}
	for i, token := range remaining {
		expectedHash := hashOf(issued[i+2])
		if token.TokenHash != expectedHash {
			t.Fatalf("expected remaining token %d to be issued token %d", i, i+2)
		}
	}
}

func TestListAccessTokensExposesAudience(t *testing.T) {
	DB := setupBrowserTokenTestDB(t)
	user := createBrowserTokenTestUser(t, DB, "list-user")

	createBrowserTokenTestAccessToken(t, DB, user, func(token *database.AccessToken) {
		token.Name = "browser:browser-api|session"
		token.Audience = database.AudienceBrowserAPI
		token.Scopes = database.JoinTokenScopes([]string{database.ScopeBotsRead})
		token.ExpiresAt = func() *time.Time { ts := time.Now().Add(time.Hour); return &ts }()
	})
	// The user also has an auto-provisioned default regular token.

	req := httptest.NewRequest("GET", "/api/v1/user/access-tokens/list?page=1&limit=50", nil)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h := &UserHandler{}
	h.ListAccessTokens(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200 listing tokens, got %d: %s", rec.Code, rec.Body.String())
	}

	var response AccessTokensListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}

	browserCount := 0
	regularCount := 0
	for _, row := range response.Rows {
		switch row.Audience {
		case database.AudienceBrowserAPI:
			browserCount++
			if row.Scopes != database.ScopeBotsRead {
				t.Fatalf("expected scopes %q on browser token row, got %q", database.ScopeBotsRead, row.Scopes)
			}
		case "":
			regularCount++
		default:
			t.Fatalf("unexpected audience %q", row.Audience)
		}
	}
	if browserCount != 1 || regularCount != 1 {
		t.Fatalf("expected 1 browser and 1 regular token row, got browser=%d regular=%d", browserCount, regularCount)
	}
}
