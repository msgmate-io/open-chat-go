package user

import (
	"backend/database"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

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

func createCLIAuthSessionUser(t *testing.T, DB *gorm.DB, name string) (*database.User, string) {
	t.Helper()
	user := database.User{Name: name, Username: name, Email: name + "@example.com"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	token := strings.Repeat("s", 43)
	session := database.Session{UserId: user.ID, Token: token, Expiry: time.Now().Add(time.Hour)}
	if err := DB.Create(&session).Error; err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	return &user, token
}

func cliAuthRequest(target string, db *gorm.DB, sessionToken string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, bytes.NewReader(nil))
	req.Host = "open-chat.example"
	if db != nil {
		req = req.WithContext(context.WithValue(req.Context(), "db", db))
	}
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: "session_id", Value: sessionToken})
	}
	return req
}

func decodeCLIAuthJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rec.Body.String())
	}
	return out
}

func grantCLIAuthTokenPermission(t *testing.T, DB *gorm.DB, user *database.User) {
	t.Helper()
	admin := database.User{Name: "cli-auth-admin", Username: "cli-auth-admin", Email: "cli-auth-admin@example.com"}
	if err := DB.Create(&admin).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}
	if err := DB.Model(&admin).Association("Permissions").Append(&database.Permission{Name: database.PermissionCreateAPITokens}); err != nil {
		t.Fatalf("failed to grant admin permission: %v", err)
	}
	if err := DB.Model(&user).Association("Permissions").Append(&database.Permission{Name: database.PermissionCreateAPITokens}); err != nil {
		t.Fatalf("failed to grant token permission: %v", err)
	}
}

func startCLIAuthFlow(t *testing.T, DB *gorm.DB) (string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h := &UserHandler{}
	h.CLIBrowserAuthStart(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/start", DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("cli-auth/start status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	state := decodeCLIAuthJSON(t, rec)["state"].(string)
	if state == "" {
		t.Fatalf("cli-auth/start returned an empty state")
	}
	cliBrowserAuthResults.mu.Lock()
	result, ok := cliBrowserAuthResults.state[state]
	cliBrowserAuthResults.mu.Unlock()
	if !ok {
		t.Fatalf("issued state %q not found in the store", state)
	}
	codeVerifier := result.CodeVerifier
	if codeVerifier == "" {
		t.Fatalf("issued state %q has no paired code verifier", state)
	}
	return state, codeVerifier
}

func TestCLIBrowserAuthStartIssuesServerState(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupCLIAuthTestDB(t)
	h := &UserHandler{}

	rec := httptest.NewRecorder()
	h.CLIBrowserAuthStart(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/start", DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	state := decodeCLIAuthJSON(t, rec)["state"].(string)
	if !isValidCLIAuthState(state) {
		t.Fatalf("issued state %q is not a valid CLI auth state", state)
	}
	if len(state) != 32 {
		t.Fatalf("issued state length = %d, want 32", len(state))
	}

	// The same request must not produce the same state twice.
	rec2 := httptest.NewRecorder()
	h.CLIBrowserAuthStart(rec2, cliAuthRequest("https://open-chat.example/api/user/cli-auth/start", DB, ""))
	state2 := decodeCLIAuthJSON(t, rec2)["state"].(string)
	if state == state2 {
		t.Fatalf("two /cli-auth/start calls returned the same state %q", state)
	}

	// The start endpoint must not mint a token and must not require a session.
	cliBrowserAuthResults.mu.Lock()
	pending := cliBrowserAuthResults.state[state]
	cliBrowserAuthResults.mu.Unlock()
	if pending.Token != "" || pending.Error != "" {
		t.Fatalf("start stored a result prematurely: %+v", pending)
	}
}

func TestCLIBrowserAuthRejectsAttackerSuppliedState(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupCLIAuthTestDB(t)
	user, sessionToken := createCLIAuthSessionUser(t, DB, "cli-victim")
	grantCLIAuthTokenPermission(t, DB, user)
	h := &UserHandler{}

	// This is the attack from ci#936: the caller picks a state they know and
	// mints a token under it. Without the pairing secret it must be rejected.
	attackerState := "attacker-chosen-state-value-1234"
	rec := httptest.NewRecorder()
	h.CLIBrowserAuth(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth?state="+attackerState+"&name=evil", DB, sessionToken))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("attacker state status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}

	// Even with a guessed pairing secret, a state that was never issued by the
	// server must be rejected.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuth(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth?state="+attackerState+"&code_verifier=guessed-secret-1234", DB, sessionToken))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("attacker state with guessed secret status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}

	// An attacker can poll the same guessed state, but must never receive a
	// token: nothing was minted under it.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuthPoll(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/poll?state="+attackerState+"&code_verifier=guessed-secret-1234", DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, want 200", rec.Code)
	}
	if body := decodeCLIAuthJSON(t, rec); body["ready"] != false {
		t.Fatalf("attacker poll must not yield a token: %v", body)
	}

	// No token row may exist for the victim.
	var count int64
	if err := DB.Model(&database.AccessToken{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count tokens: %v", err)
	}
	if count != 0 {
		t.Fatalf("attacker flow minted %d tokens for the victim, want 0", count)
	}
}

func TestCLIBrowserAuthFlowWithPairedSecret(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupCLIAuthTestDB(t)
	user, sessionToken := createCLIAuthSessionUser(t, DB, "cli-happy")
	grantCLIAuthTokenPermission(t, DB, user)
	h := &UserHandler{}

	state, codeVerifier := startCLIAuthFlow(t, DB)

	// The victim opens the authorize link with the paired secret.
	authURL := fmt.Sprintf("https://open-chat.example/api/user/cli-auth?state=%s&code_verifier=%s&name=my-cli", state, codeVerifier)
	rec := httptest.NewRecorder()
	h.CLIBrowserAuth(rec, cliAuthRequest(authURL, DB, sessionToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorize status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// A poller without the secret gets nothing, and the result survives so the
	// legitimate poller can still collect it.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuthPoll(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/poll?state="+state, DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("unauthorized poll status = %d, want 200", rec.Code)
	}
	if body := decodeCLIAuthJSON(t, rec); body["ready"] != false {
		t.Fatalf("poll without secret must not yield a token: %v", body)
	}

	// The legitimate poller presents the secret and collects the token.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuthPoll(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/poll?state="+state+"&code_verifier="+codeVerifier, DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized poll status = %d, want 200", rec.Code)
	}
	body := decodeCLIAuthJSON(t, rec)
	if body["ready"] != true {
		t.Fatalf("authorized poll must be ready: %v", body)
	}
	rawToken, _ := body["token"].(string)
	if !strings.HasPrefix(rawToken, "ocat_") {
		t.Fatalf("expected an ocat_ token, got %q", rawToken)
	}

	// The minted token must expire (ci#936: the token used to never expire).
	var stored database.AccessToken
	if err := DB.Where("user_id = ? AND name = ?", user.ID, "my-cli").First(&stored).Error; err != nil {
		t.Fatalf("failed to load minted token: %v", err)
	}
	if stored.ExpiresAt == nil {
		t.Fatalf("minted CLI token has no expiry")
	}
	if !stored.ExpiresAt.After(time.Now()) || stored.ExpiresAt.After(time.Now().Add(cliBrowserAuthTokenTTL+time.Minute)) {
		t.Fatalf("CLI token expiry %v not within the %s window", stored.ExpiresAt, cliBrowserAuthTokenTTL)
	}

	// The state is consumed by the successful poll: a second authorized poll
	// must not yield the token again.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuthPoll(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/poll?state="+state+"&code_verifier="+codeVerifier, DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("second poll status = %d, want 200", rec.Code)
	}
	if body := decodeCLIAuthJSON(t, rec); body["ready"] != false {
		t.Fatalf("state must be consumed after a successful poll: %v", body)
	}
}

func TestCLIBrowserAuthMintsOnlyForPendingServerState(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupCLIAuthTestDB(t)
	user, sessionToken := createCLIAuthSessionUser(t, DB, "cli-replay")
	grantCLIAuthTokenPermission(t, DB, user)
	h := &UserHandler{}

	state, codeVerifier := startCLIAuthFlow(t, DB)

	// Authorize once with the correct secret: the state now holds a result.
	rec := httptest.NewRecorder()
	h.CLIBrowserAuth(rec, cliAuthRequest(fmt.Sprintf("https://open-chat.example/api/user/cli-auth?state=%s&code_verifier=%s", state, codeVerifier), DB, sessionToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("first authorize status = %d, want 200", rec.Code)
	}

	// Replaying the same authorize link must not mint a second token: the
	// state already holds a result.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuth(rec, cliAuthRequest(fmt.Sprintf("https://open-chat.example/api/user/cli-auth?state=%s&code_verifier=%s", state, codeVerifier), DB, sessionToken))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("replayed authorize status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	var count int64
	if err := DB.Model(&database.AccessToken{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("replayed authorize minted %d tokens, want 1", count)
	}
}

func TestCLIBrowserAuthWrongSecretDoesNotConsumeResult(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupCLIAuthTestDB(t)
	user, sessionToken := createCLIAuthSessionUser(t, DB, "cli-wrong-secret")
	grantCLIAuthTokenPermission(t, DB, user)
	h := &UserHandler{}

	state, codeVerifier := startCLIAuthFlow(t, DB)

	rec := httptest.NewRecorder()
	h.CLIBrowserAuth(rec, cliAuthRequest(fmt.Sprintf("https://open-chat.example/api/user/cli-auth?state=%s&code_verifier=%s", state, codeVerifier), DB, sessionToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorize status = %d, want 200", rec.Code)
	}

	// A wrong secret must not consume the result.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuthPoll(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/poll?state="+state+"&code_verifier=wrong-secret-value", DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("wrong-secret poll status = %d, want 200", rec.Code)
	}
	if body := decodeCLIAuthJSON(t, rec); body["ready"] != false {
		t.Fatalf("wrong secret must not yield a token: %v", body)
	}

	// The correct secret still collects the token.
	rec = httptest.NewRecorder()
	h.CLIBrowserAuthPoll(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/poll?state="+state+"&code_verifier="+codeVerifier, DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("correct-secret poll status = %d, want 200", rec.Code)
	}
	if body := decodeCLIAuthJSON(t, rec); body["ready"] != true {
		t.Fatalf("correct secret must yield the token: %v", body)
	}
}

func TestCLIBrowserAuthPollRequiresServerState(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupCLIAuthTestDB(t)
	h := &UserHandler{}

	// Polling a state that was never issued must never return a token, even
	// with a plausible-looking secret.
	rec := httptest.NewRecorder()
	h.CLIBrowserAuthPoll(rec, cliAuthRequest("https://open-chat.example/api/user/cli-auth/poll?state=never-issued-state-123456&code_verifier=never-issued-secret", DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, want 200", rec.Code)
	}
	if body := decodeCLIAuthJSON(t, rec); body["ready"] != false {
		t.Fatalf("unknown state must not yield a token: %v", body)
	}
}
