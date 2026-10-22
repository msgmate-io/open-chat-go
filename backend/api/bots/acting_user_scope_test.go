package bots

import (
	"backend/database"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestListAutomatedSessionSeesOwnerFleet covers ci#824: an automated bot session
// owns no bot_runtime_owners row itself, so listing by the raw principal returned
// an empty page. The list must resolve the acting user (the runtime owner) and
// must not leak a stranger's runtime.
func TestListAutomatedSessionSeesOwnerFleet(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.acting2@example.com", false)
	ownerRuntime, _ := createBotRuntimeForTest(t, DB, owner, "acting-owner-bot2")

	stranger := createUserForBotsTest(t, DB, "stranger.acting2@example.com", false)
	strangerRuntime, _ := createBotRuntimeForTest(t, DB, stranger, "acting-stranger-bot2")

	_, botUser := createBotRuntimeForTest(t, DB, owner, "acting-session-bot")

	req := httptest.NewRequest("GET", "/api/v1/bots/list?limit=50", nil)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", botUser)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h := &BotsHandler{}
	h.List(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var page ListedBotsPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(page.Rows) == 0 {
		t.Fatalf("expected a non-empty bot list for an automated session")
	}
	uuids := map[string]bool{}
	for _, row := range page.Rows {
		uuids[row.UUID] = true
	}
	if !uuids[ownerRuntime.UUID] {
		t.Fatalf("expected owner runtime %s in list, got %#v", ownerRuntime.UUID, uuids)
	}
	if uuids[strangerRuntime.UUID] {
		t.Fatalf("automated session leaked a stranger's runtime %s", strangerRuntime.UUID)
	}
}

func TestGetResolvesActingUserForAutomatedSession(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.acting3@example.com", false)
	ownerRuntime, _ := createBotRuntimeForTest(t, DB, owner, "acting-owner-bot3")

	stranger := createUserForBotsTest(t, DB, "stranger.acting3@example.com", false)
	strangerRuntime, _ := createBotRuntimeForTest(t, DB, stranger, "acting-stranger-bot3")

	_, botUser := createBotRuntimeForTest(t, DB, owner, "acting-session-bot3")

	get := func(identifier string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/bots/"+identifier, nil)
		req.SetPathValue("identifier", identifier)
		ctx := context.WithValue(req.Context(), "db", DB)
		ctx = context.WithValue(ctx, "user", botUser)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()
		h := &BotsHandler{}
		h.Get(rr, req)
		return rr
	}

	if rr := get(ownerRuntime.UUID); rr.Code != 200 {
		t.Fatalf("expected 200 for owner runtime by uuid, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr := get(ownerRuntime.Name); rr.Code != 200 {
		t.Fatalf("expected 200 for owner runtime by name, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr := get(strangerRuntime.UUID); rr.Code != 404 {
		t.Fatalf("expected 404 for a stranger's runtime, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestCreateInteractionResolvesActingUserForAutomatedSession covers the third
// owner-scoped route in ci#824: an automated session must be able to start an
// interaction with its owner's runtime, and the resulting chat must belong to the
// owner rather than to the bot principal.
func TestCreateInteractionResolvesActingUserForAutomatedSession(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.acting4@example.com", false)
	ownerRuntime, _ := createBotRuntimeForTest(t, DB, owner, "acting-owner-bot4")

	stranger := createUserForBotsTest(t, DB, "stranger.acting4@example.com", false)
	strangerRuntime, _ := createBotRuntimeForTest(t, DB, stranger, "acting-stranger-bot4")

	_, botUser := createBotRuntimeForTest(t, DB, owner, "acting-session-bot4")

	queueClient, queueInspector, cleanupQueue := setupAsynqTest(t)
	defer cleanupQueue()

	post := func(bot BotDTO) *httptest.ResponseRecorder {
		return postInteractionForTest(t, DB, botUser, bot, CreateBotInteractionRequest{Message: "hello from bot session"}, queueClient, queueInspector)
	}

	if rr := post(toDTO(strangerRuntime)); rr.Code != 404 {
		t.Fatalf("expected 404 for a stranger's runtime, got %d: %s", rr.Code, rr.Body.String())
	}

	rr := post(toDTO(ownerRuntime))
	if rr.Code != 200 {
		t.Fatalf("expected 200 starting an interaction as an automated session, got %d: %s", rr.Code, rr.Body.String())
	}

	var response BotInteractionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode interaction response: %v", err)
	}
	if response.ChatUUID == "" {
		t.Fatalf("expected chat_uuid in interaction response")
	}

	var chat database.Chat
	if err := DB.Where("uuid = ?", response.ChatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to be created: %v", err)
	}
	if chat.User1Id != owner.ID && chat.User2Id != owner.ID {
		t.Fatalf("expected interaction chat to belong to owner %d, got chat %#v", owner.ID, chat)
	}
}
