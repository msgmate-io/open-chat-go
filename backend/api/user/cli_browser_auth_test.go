package user

import (
	"backend/database"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// ---- shared helpers ---------------------------------------------------------

func setupCLIAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "cli_auth_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupTestDatabase(t, cfg)
}

func createCLIAuthTestUser(t *testing.T, DB *gorm.DB, name string) *database.User {
	t.Helper()
	user := database.User{
		Name:     name,
		Username: name,
		Email:    name + "@example.com",
		IsAdmin:  true,
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	return &user
}

// createCLIAuthTestSession wires a session cookie for the given user so the
// CLI browser-authorization endpoint can resolve the user server-side.
func createCLIAuthTestSession(t *testing.T, DB *gorm.DB, user *database.User) string {
	t.Helper()
	session := database.Session{
		UserId: user.ID,
		Token:  "cli-auth-session-token-" + user.Username,
		Expiry: time.Now().Add(time.Hour),
	}
	if err := DB.Create(&session).Error; err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	return session.Token
}

// newCLIAuthStartRequest builds a GET /api/user/cli-auth request carrying the
// session cookie of the given user.
func newCLIAuthStartRequest(DB *gorm.DB, user *database.User, sessionToken string) *http.Request {
	req := httptest.NewRequest("GET", "/api/user/cli-auth", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: sessionToken})
	ctx := context.WithValue(req.Context(), "db", DB)
	return req.WithContext(ctx)
}

// runCLIAuthStart runs the CLI browser-authorization start handler and returns
// the response recorder plus the raw token it minted (empty on failure).
func runCLIAuthStart(DB *gorm.DB, user *database.User, sessionToken, state string) (*httptest.ResponseRecorder, string) {
	req := newCLIAuthStartRequest(DB, user, sessionToken)
	if state != "" {
		q := req.URL.Query()
		q.Set("state", state)
		req.URL.RawQuery = q.Encode()
	}
	rec := httptest.NewRecorder()
	h := &UserHandler{}
	h.CLIBrowserAuth(rec, req)
	return rec, parseRawTokenFromCLIAuthPage(rec)
}

// parseRawTokenFromCLIAuthPage extracts the raw token from the success page
// body the start handler renders for a non-redirect flow.
func parseRawTokenFromCLIAuthPage(rec *httptest.ResponseRecorder) string {
	body := rec.Body.String()
	const marker = "token="
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(marker):]
	if end := strings.IndexAny(rest, " \t\n\r\"'&<"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func runCLIAuthPoll(DB *gorm.DB, state, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/user/cli-auth/poll", nil)
	if state != "" {
		q := req.URL.Query()
		q.Set("state", state)
		req.URL.RawQuery = q.Encode()
	}
	if secret != "" {
		q := req.URL.Query()
		q.Set("secret", secret)
		req.URL.RawQuery = q.Encode()
	}
	ctx := context.WithValue(req.Context(), "db", DB)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h := &UserHandler{}
	h.CLIBrowserAuthPoll(rec, req)
	return rec
}

// ---- tests ------------------------------------------------------------------

// TestCLIAuthStateIsServerGenerated pins the ci#936 fix: the CLI browser-auth
// flow must not accept an attacker-chosen ?state= that later lets the attacker
// claim the minted token by polling with only that state.
func TestCLIAuthStateIsServerGenerated(t *testing.T) {
	DB := setupCLIAuthTestDB(t)
	user := createCLIAuthTestUser(t, DB, "state-user")
	sessionToken := createCLIAuthTestSession(t, DB, user)

	rec, _ := runCLIAuthStart(DB, user, sessionToken, "attacker-chosen-state-value")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for attacker-chosen state, got %d: %s", rec.Code, rec.Body.String())
	}

	// The result must never have been published for the attacker's state: a
	// poll for that state stays not-ready even after a valid flow completed.
	attackerPoll := runCLIAuthPoll(DB, "attacker-chosen-state-value", "")
	if attackerPoll.Code != http.StatusOK {
		t.Fatalf("expected 200 poll response, got %d", attackerPoll.Code)
	}
	var attackerBody map[string]any
	if err := json.Unmarshal(attackerPoll.Body.Bytes(), &attackerBody); err != nil {
		t.Fatalf("failed to decode poll response: %v", err)
	}
	if ready, _ := attackerBody["ready"].(bool); ready {
		t.Fatalf("attacker-chosen state must never publish a result, got ready=true")
	}
}

// TestCLIAuthPollRequiresPairedSecret pins the ci#936 fix: the poll endpoint
// hands the minted token only to the caller who presents the paired secret,
// never to anyone who merely knows the (server-generated) state.
func TestCLIAuthPollRequiresPairedSecret(t *testing.T) {
	DB := setupCLIAuthTestDB(t)
	user := createCLIAuthTestUser(t, DB, "secret-user")
	sessionToken := createCLIAuthTestSession(t, DB, user)

	rec, rawToken := runCLIAuthStart(DB, user, sessionToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from cli-auth start, got %d: %s", rec.Code, rec.Body.String())
	}
	if rawToken == "" {
		t.Fatalf("expected a minted token on the success page, got empty")
	}

	state, secret := findCLIAuthResult(t)
	if state == "" {
		t.Fatalf("expected a server-generated flow state")
	}
	if secret == "" {
		t.Fatalf("expected a paired poll secret")
	}

	t.Run("state without secret rejected", func(t *testing.T) {
		rec := runCLIAuthPoll(DB, state, "")
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode poll response: %v", err)
		}
		if ready, _ := body["ready"].(bool); ready {
			t.Fatalf("poll without the paired secret must not become ready")
		}
		if token, _ := body["token"].(string); token != "" {
			t.Fatalf("poll without the paired secret must not leak the token, got %q", token)
		}
	})

	t.Run("wrong secret rejected", func(t *testing.T) {
		rec := runCLIAuthPoll(DB, state, "wrong-secret")
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode poll response: %v", err)
		}
		if ready, _ := body["ready"].(bool); ready {
			t.Fatalf("poll with the wrong secret must not become ready")
		}
	})

	t.Run("correct secret succeeds", func(t *testing.T) {
		rec := runCLIAuthPoll(DB, state, secret)
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode poll response: %v", err)
		}
		if ready, _ := body["ready"].(bool); !ready {
			t.Fatalf("expected ready=true with the paired secret, got %s", rec.Body.String())
		}
		gotToken, _ := body["token"].(string)
		if gotToken != rawToken {
			t.Fatalf("expected the minted token back from the poll, got %q (want %q)", gotToken, rawToken)
		}
	})
}

// TestCLIAuthMintedTokenIsShortLived pins the ci#936 fix: the account token
// minted by the CLI browser-auth flow must carry a short expiry instead of
// being a never-expiring credential.
func TestCLIAuthMintedTokenIsShortLived(t *testing.T) {
	DB := setupCLIAuthTestDB(t)
	user := createCLIAuthTestUser(t, DB, "expiry-user")
	sessionToken := createCLIAuthTestSession(t, DB, user)

	rec, rawToken := runCLIAuthStart(DB, user, sessionToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from cli-auth start, got %d: %s", rec.Code, rec.Body.String())
	}
	if rawToken == "" {
		t.Fatalf("expected a minted token on the success page, got empty")
	}

	var stored database.AccessToken
	if err := DB.Where("token_hash = ?", hashRawTokenForTest(t, rawToken)).First(&stored).Error; err != nil {
		t.Fatalf("failed to load minted token from DB: %v", err)
	}
	if stored.ExpiresAt == nil {
		t.Fatalf("expected the minted CLI token to carry an expiry, got nil")
	}
	if !stored.ExpiresAt.After(time.Now()) {
		t.Fatalf("expected the minted CLI token expiry %s to be in the future", stored.ExpiresAt.Format(time.RFC3339))
	}
	if stored.ExpiresAt.After(time.Now().Add(cliBrowserAuthTokenTTL + time.Minute)) {
		t.Fatalf("expected the minted CLI token expiry %s to stay within the short TTL %s",
			stored.ExpiresAt.Format(time.RFC3339), cliBrowserAuthTokenTTL)
	}
}

// findCLIAuthResult returns the (state, secret) pair the test flow published,
// so tests can prove the poll requires the paired secret.
func findCLIAuthResult(t *testing.T) (string, string) {
	t.Helper()
	cliBrowserAuthResults.mu.Lock()
	defer cliBrowserAuthResults.mu.Unlock()
	for state, result := range cliBrowserAuthResults.state {
		if result.Secret == "" {
			continue
		}
		return state, result.Secret
	}
	return "", ""
}

func hashRawTokenForTest(t *testing.T, raw string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
