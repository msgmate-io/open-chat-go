package bots

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

// TestProbeCreateInteractionIdentityResolves tests the probe's happy path: for
// an acting user that can read the bot, the probe must answer 200 with
// `resolves=true` and the identifiers that the create-interaction handler
// would act for (ci#865).
func TestProbeCreateInteractionIdentityResolves(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.probe.resolve@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "probe-resolve-bot")

	rr := getProbeCreateInteraction(t, DB, owner, bot.UUID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	var response BotIdentityProbeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("body %q is not valid JSON: %v", rr.Body.String(), err)
	}
	if !response.Resolves {
		t.Fatalf("expected resolves=true, got %#v", response)
	}
	if response.BotUUID != bot.UUID {
		t.Fatalf("bot_uuid = %q, want %q", response.BotUUID, bot.UUID)
	}
	if response.BotName == "" {
		t.Fatal("expected bot_name to be populated")
	}
	if response.ActingUserUUID != owner.UUID {
		t.Fatalf("acting_user_uuid = %q, want %q", response.ActingUserUUID, owner.UUID)
	}
	if response.ActingUserUsername == "" {
		t.Fatal("expected acting_user_username to be populated")
	}
}

// TestProbeCreateInteractionIdentityUnknownBot tests the resolvable-negative
// path: an identifier the acting user cannot act for must still answer 200
// (never 404/500) so the canary can tell "identity lookup ok, no such bot"
// apart from "backend create-interaction failure".
func TestProbeCreateInteractionIdentityUnknownBot(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.probe.unknown@example.com", false)

	rr := getProbeCreateInteraction(t, DB, owner, "does-not-exist")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	var response BotIdentityProbeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("body %q is not valid JSON: %v", rr.Body.String(), err)
	}
	if response.Resolves {
		t.Fatalf("expected resolves=false for unknown bot, got %#v", response)
	}
}

// TestProbeCreateInteractionIdentityEnvelopeAlwaysJSON pins the canary
// contract: the probe must always return a parseable JSON body, and a
// database/context failure must surface as a JSON envelope with its own code,
// never an empty response (the canary calls response.json() unconditionally).
func TestProbeCreateInteractionIdentityEnvelopeAlwaysJSON(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.probe.envelope@example.com", false)

	// Missing DB context: the handler answers a JSON envelope, not a blank 500.
	req := httptest.NewRequest("GET", "/api/v1/bots/anything/probe-create-interaction", nil)
	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.ProbeCreateInteractionIdentity(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing DB status = %d, want 400: %s", rr.Code, rr.Body.String())
	}

	var env struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q is not valid JSON: %v", rr.Body.String(), err)
	}
	if env.Code == "" || env.Error == "" {
		t.Fatalf("expected code and error in envelope, got %#v", env)
	}

	// A non-existent bot with a live DB still answers 200 with a JSON body.
	rr = getProbeCreateInteraction(t, DB, owner, "no-such-bot")
	if rr.Code != http.StatusOK {
		t.Fatalf("unknown bot status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if strings.TrimSpace(rr.Body.String()) == "" {
		t.Fatal("empty body for unknown bot: the canary cannot parse this (ci#865)")
	}
	var response BotIdentityProbeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("body %q is not valid JSON: %v", rr.Body.String(), err)
	}
	if response.Resolves {
		t.Fatalf("expected resolves=false, got %#v", response)
	}
}

func getProbeCreateInteraction(t *testing.T, DB *gorm.DB, user *database.User, identifier string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/bots/"+identifier+"/probe-create-interaction", nil)
	req.SetPathValue("identifier", identifier)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.ProbeCreateInteractionIdentity(rr, req)
	return rr
}