package user

import (
	"backend/database"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func setupOAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "oauth_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupTestDatabase(t, cfg)
}

func createOAuthSessionUser(t *testing.T, DB *gorm.DB, name string) (*database.User, string) {
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

func oauthRequest(method, target string, body []byte, db *gorm.DB, sessionToken string) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Host = "open-chat.example"
	if db != nil {
		req = req.WithContext(context.WithValue(req.Context(), "db", db))
	}
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: "session_id", Value: sessionToken})
	}
	return req
}

func oauthFormRequest(method, target string, form url.Values, db *gorm.DB, sessionToken string) *http.Request {
	req := oauthRequest(method, target, []byte(form.Encode()), db, sessionToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func pkcePair(verifier string) (string, string) {
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func decodeOAuthJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rec.Body.String())
	}
	return out
}

func TestOAuthDiscoveryDocumentsShape(t *testing.T) {
	resetOAuthStoresForTest()
	h := &UserHandler{}

	rec := httptest.NewRecorder()
	h.ProtectedResourceMetadata(rec, oauthRequest("GET", "https://open-chat.example/.well-known/oauth-protected-resource", nil, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("protected-resource status = %d, want 200", rec.Code)
	}
	prm := decodeOAuthJSON(t, rec)
	if prm["resource"] != "https://open-chat.example" {
		t.Errorf("resource = %v", prm["resource"])
	}
	servers, ok := prm["authorization_servers"].([]interface{})
	if !ok || len(servers) != 1 || servers[0] != "https://open-chat.example" {
		t.Errorf("authorization_servers = %v", prm["authorization_servers"])
	}
	methods, _ := prm["bearer_methods_supported"].([]interface{})
	if len(methods) != 1 || methods[0] != "header" {
		t.Errorf("bearer_methods_supported = %v", prm["bearer_methods_supported"])
	}

	rec = httptest.NewRecorder()
	h.AuthorizationServerMetadata(rec, oauthRequest("GET", "https://open-chat.example/.well-known/oauth-authorization-server", nil, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorization-server status = %d, want 200", rec.Code)
	}
	asm := decodeOAuthJSON(t, rec)
	if asm["issuer"] != "https://open-chat.example" {
		t.Errorf("issuer = %v", asm["issuer"])
	}
	if asm["authorization_endpoint"] != "https://open-chat.example/authorize" {
		t.Errorf("authorization_endpoint = %v", asm["authorization_endpoint"])
	}
	if asm["token_endpoint"] != "https://open-chat.example/token" {
		t.Errorf("token_endpoint = %v", asm["token_endpoint"])
	}
	if asm["registration_endpoint"] != "https://open-chat.example/register" {
		t.Errorf("registration_endpoint = %v", asm["registration_endpoint"])
	}
	pkce, _ := asm["code_challenge_methods_supported"].([]interface{})
	if len(pkce) != 1 || pkce[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v", asm["code_challenge_methods_supported"])
	}
	authMethods, _ := asm["token_endpoint_auth_methods_supported"].([]interface{})
	if len(authMethods) != 1 || authMethods[0] != "none" {
		t.Errorf("token_endpoint_auth_methods_supported = %v", asm["token_endpoint_auth_methods_supported"])
	}
}

func TestOAuthRegisterPinsRedirectURIs(t *testing.T) {
	resetOAuthStoresForTest()
	h := &UserHandler{}
	body, _ := json.Marshal(map[string]interface{}{
		"client_name":   "test-mcp",
		"redirect_uris": []string{"http://localhost:7777/callback"},
	})
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, oauthRequest("POST", "/register", body, nil, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	resp := decodeOAuthJSON(t, rec)
	clientID, _ := resp["client_id"].(string)
	if clientID == "" {
		t.Fatalf("missing client_id in %v", resp)
	}
	if resp["token_endpoint_auth_method"] != "none" {
		t.Errorf("token_endpoint_auth_method = %v", resp["token_endpoint_auth_method"])
	}
	client, ok := lookupOAuthClient(clientID)
	if !ok {
		t.Fatalf("registered client %q not found", clientID)
	}
	if !isValidRedirectURI(client, "http://localhost:7777/callback") {
		t.Errorf("pinned redirect_uri missing")
	}
	if isValidRedirectURI(client, "http://localhost:7777/evil") {
		t.Errorf("unexpected redirect_uri allowed")
	}

	rec = httptest.NewRecorder()
	badBody, _ := json.Marshal(map[string]interface{}{"redirect_uris": []string{"not-a-url"}})
	h.RegisterOAuthClient(rec, oauthRequest("POST", "/register", badBody, nil, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid redirect_uri status = %d, want 400", rec.Code)
	}
}

func TestOAuthAuthorizeFlowAndTokenExchange(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupOAuthTestDB(t)
	_, sessionToken := createOAuthSessionUser(t, DB, "oauth-user")
	h := &UserHandler{}

	redirectURI := "http://localhost:8085/callback"
	regBody, _ := json.Marshal(map[string]interface{}{"redirect_uris": []string{redirectURI}})
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, oauthRequest("POST", "/register", regBody, nil, ""))
	clientID := decodeOAuthJSON(t, rec)["client_id"].(string)

	verifier, challenge := pkcePair("verifier-abcdefghijklmnopqrstuvwxyz-0123456789")
	authorizeURL := "/authorize?" + url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"state":                 {"xyz-state"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	rec = httptest.NewRecorder()
	h.AuthorizeOAuth(rec, oauthRequest("GET", authorizeURL, nil, DB, sessionToken))
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302 (body=%s)", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, redirectURI) {
		t.Fatalf("unexpected redirect location %q", location)
	}
	parsed, _ := url.Parse(location)
	if parsed.Query().Get("state") != "xyz-state" {
		t.Errorf("state not echoed: %q", location)
	}
	code := parsed.Query().Get("code")
	if code == "" {
		t.Fatalf("no authorization code in %q", location)
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	rec = httptest.NewRecorder()
	h.ExchangeOAuthToken(rec, oauthFormRequest("POST", "/token", form, DB, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	tokenResp := decodeOAuthJSON(t, rec)
	rawToken, _ := tokenResp["access_token"].(string)
	if rawToken == "" {
		t.Fatalf("missing access_token in %v", tokenResp)
	}
	if tokenResp["token_type"] != "Bearer" {
		t.Errorf("token_type = %v", tokenResp["token_type"])
	}

	var stored database.AccessToken
	if err := DB.Where("audience = ?", database.AudienceMCPOAuth).First(&stored).Error; err != nil {
		t.Fatalf("minted token not persisted with mcp-oauth audience: %v", err)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.After(time.Now()) {
		t.Errorf("minted token has no future expiry")
	}

	// Single-use: the same code must not mint a second token.
	rec = httptest.NewRecorder()
	h.ExchangeOAuthToken(rec, oauthFormRequest("POST", "/token", form, DB, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code reuse status = %d, want 400", rec.Code)
	}
	if decodeOAuthJSON(t, rec)["error"] != "invalid_grant" {
		t.Errorf("expected invalid_grant on reuse, got %v", rec.Body.String())
	}
}

func TestOAuthAuthorizeRejectsUnregisteredRedirectURI(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupOAuthTestDB(t)
	_, sessionToken := createOAuthSessionUser(t, DB, "oauth-user")
	h := &UserHandler{}

	regBody, _ := json.Marshal(map[string]interface{}{"redirect_uris": []string{"http://localhost:8085/callback"}})
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, oauthRequest("POST", "/register", regBody, nil, ""))
	clientID := decodeOAuthJSON(t, rec)["client_id"].(string)

	_, challenge := pkcePair("verifier-abcdefghijklmnopqrstuvwxyz-0123456789")
	authorizeURL := "/authorize?" + url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost:9999/other"},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	rec = httptest.NewRecorder()
	h.AuthorizeOAuth(rec, oauthRequest("GET", authorizeURL, nil, DB, sessionToken))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unregistered redirect_uri status = %d, want 400", rec.Code)
	}
}

func TestOAuthAuthorizeRedirectsToLoginWhenUnauthenticated(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupOAuthTestDB(t)
	h := &UserHandler{}

	regBody, _ := json.Marshal(map[string]interface{}{"redirect_uris": []string{"http://localhost:8085/callback"}})
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, oauthRequest("POST", "/register", regBody, nil, ""))
	clientID := decodeOAuthJSON(t, rec)["client_id"].(string)

	_, challenge := pkcePair("verifier-abcdefghijklmnopqrstuvwxyz-0123456789")
	authorizeURL := "/authorize?" + url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost:8085/callback"},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	rec = httptest.NewRecorder()
	h.AuthorizeOAuth(rec, oauthRequest("GET", authorizeURL, nil, DB, ""))
	if rec.Code != http.StatusFound {
		t.Fatalf("unauthenticated authorize status = %d, want 302", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/login?redirect=") {
		t.Fatalf("expected login redirect, got %q", rec.Header().Get("Location"))
	}
}

func TestOAuthTokenRejectsVerifierMismatch(t *testing.T) {
	resetOAuthStoresForTest()
	DB := setupOAuthTestDB(t)
	_, sessionToken := createOAuthSessionUser(t, DB, "oauth-user")
	h := &UserHandler{}

	redirectURI := "http://localhost:8085/callback"
	regBody, _ := json.Marshal(map[string]interface{}{"redirect_uris": []string{redirectURI}})
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, oauthRequest("POST", "/register", regBody, nil, ""))
	clientID := decodeOAuthJSON(t, rec)["client_id"].(string)

	_, challenge := pkcePair("verifier-abcdefghijklmnopqrstuvwxyz-0123456789")
	authorizeURL := "/authorize?" + url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	rec = httptest.NewRecorder()
	h.AuthorizeOAuth(rec, oauthRequest("GET", authorizeURL, nil, DB, sessionToken))
	location, _ := url.Parse(rec.Header().Get("Location"))
	code := location.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {"a-totally-different-verifier-value-0123456789"},
	}
	rec = httptest.NewRecorder()
	h.ExchangeOAuthToken(rec, oauthFormRequest("POST", "/token", form, DB, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("verifier mismatch status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if decodeOAuthJSON(t, rec)["error"] != "invalid_grant" {
		t.Errorf("expected invalid_grant, got %v", rec.Body.String())
	}
}

func TestPKCES256Matches(t *testing.T) {
	verifier, challenge := pkcePair("the-quick-brown-fox-0123456789")
	if !pkceS256Matches(verifier, challenge) {
		t.Errorf("expected valid PKCE pair to match")
	}
	if pkceS256Matches("wrong", challenge) {
		t.Errorf("expected mismatch for wrong verifier")
	}
	if pkceS256Matches("", challenge) || pkceS256Matches(verifier, "") {
		t.Errorf("empty inputs must not match")
	}
}
