package bots

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

// postCreateBot drives the Create handler with the given payload and returns the
// recorder plus the decoded response body (nil when the body is not JSON).
func postCreateBot(t *testing.T, DB *gorm.DB, user *database.User, payload map[string]interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to encode payload: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/v1/bots", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.Create(rr, req)

	var decoded map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		decoded = nil
	}
	return rr, decoded
}

// persistedBotUsername loads the users.username of the bot user behind the given
// contact token.
func persistedBotUsername(t *testing.T, DB *gorm.DB, contactToken string) string {
	t.Helper()
	var botUser database.User
	if err := DB.Where("contact_token = ?", contactToken).First(&botUser).Error; err != nil {
		t.Fatalf("failed to load bot user for contact token %q: %v", contactToken, err)
	}
	return botUser.Username
}

// requireCreatedBot asserts the response is a success, does not leak a
// unique-constraint error and returns the persisted non-empty users.username.
func requireCreatedBot(t *testing.T, DB *gorm.DB, rr *httptest.ResponseRecorder, response map[string]interface{}) string {
	t.Helper()
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	bodyText := strings.ToLower(rr.Body.String())
	if strings.Contains(bodyText, "unique") || strings.Contains(bodyText, "23505") {
		t.Fatalf("bot creation leaked a unique-constraint error: %s", rr.Body.String())
	}
	if response == nil {
		t.Fatalf("expected a JSON response body, got %s", rr.Body.String())
	}
	botRaw, ok := response["bot"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected bot object in response, got %s", rr.Body.String())
	}
	contactToken, _ := botRaw["bot_contact_token"].(string)
	if contactToken == "" {
		t.Fatalf("expected bot_contact_token in response, got %s", rr.Body.String())
	}
	username := persistedBotUsername(t, DB, contactToken)
	if strings.TrimSpace(username) == "" {
		t.Fatalf("ci#1158: persisted users.username must never be empty, got %q", username)
	}
	return username
}

// TestCreateBotBlankUsernamePersistsDistinctUsernames is the ci#1158 regression
// test: creating several bots that do not send a username (exactly what
// frontend/pages/chats/bots/create/+Page.tsx does) used to persist an empty
// users.username, so the second bot tripped
// "UNIQUE constraint failed: users.username" (23505 on Postgres).
func TestCreateBotBlankUsernamePersistsDistinctUsernames(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner-ci1158@example.com", false)

	names := []string{"Support Bot", "Sales Bot", "Ops Bot"}
	seen := map[string]string{}
	for _, name := range names {
		payload := createBotRequestPayload(name)
		payload["username"] = ""

		rr, response := postCreateBot(t, DB, owner, payload)
		username := requireCreatedBot(t, DB, rr, response)

		if previousName, exists := seen[username]; exists {
			t.Fatalf("ci#1158: bots %q and %q both got username %q", previousName, name, username)
		}
		seen[username] = name
	}

	if len(seen) != len(names) {
		t.Fatalf("expected %d distinct usernames, got %d (%v)", len(names), len(seen), seen)
	}
}

// TestCreateBotSameSlugGetsDeterministicSuffix covers the collision branch of
// the derivation: two bot names that slug to the same handle must both be
// accepted and must end up with distinct usernames.
func TestCreateBotSameSlugGetsDeterministicSuffix(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner-ci1158-slug@example.com", false)

	firstPayload := createBotRequestPayload("My Bot")
	firstPayload["username"] = ""
	firstRR, firstResponse := postCreateBot(t, DB, owner, firstPayload)
	firstUsername := requireCreatedBot(t, DB, firstRR, firstResponse)

	secondPayload := createBotRequestPayload("my-bot")
	secondPayload["username"] = "   "
	secondRR, secondResponse := postCreateBot(t, DB, owner, secondPayload)
	secondUsername := requireCreatedBot(t, DB, secondRR, secondResponse)

	if firstUsername == secondUsername {
		t.Fatalf("ci#1158: colliding slugs must not share a username, both were %q", firstUsername)
	}
	if firstUsername != "my-bot" {
		t.Fatalf("expected first slug username %q, got %q", "my-bot", firstUsername)
	}
	if secondUsername != "my-bot-2" {
		t.Fatalf("expected deterministic suffix username %q, got %q", "my-bot-2", secondUsername)
	}
}

// TestCreateBotUnslugifiableNameFallsBackToGeneratedUsername asserts the
// derivation never returns an empty handle even when the name has no usable
// characters (the fallback branch).
func TestCreateBotUnslugifiableNameFallsBackToGeneratedUsername(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner-ci1158-fallback@example.com", false)

	payload := createBotRequestPayload("🤖🤖")
	payload["username"] = ""
	rr, response := postCreateBot(t, DB, owner, payload)
	username := requireCreatedBot(t, DB, rr, response)

	if !strings.HasPrefix(username, "usr_") {
		t.Fatalf("expected generated fallback username with usr_ prefix, got %q", username)
	}
}

// TestCreateBotExplicitUsernameIsHonoured makes sure the new request field is
// threaded through to the persisted row.
func TestCreateBotExplicitUsernameIsHonoured(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner-ci1158-explicit@example.com", false)

	payload := createBotRequestPayload("Explicit Bot")
	payload["username"] = "Explicit-Handle"
	rr, response := postCreateBot(t, DB, owner, payload)
	username := requireCreatedBot(t, DB, rr, response)

	if username != "explicit-handle" {
		t.Fatalf("expected slugged explicit username %q, got %q", "explicit-handle", username)
	}
}
