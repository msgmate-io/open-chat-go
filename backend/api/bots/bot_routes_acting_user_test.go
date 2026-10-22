package bots

import (
	"backend/database"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"gorm.io/gorm"
)

// botRouteRequest drives a canonical bot route as an arbitrary session principal
// (optionally with an asynq queue in the context for interaction routes).
func botRouteRequest(t *testing.T, DB *gorm.DB, sessionUser *database.User, method, path string, pathValues map[string]string, body interface{}, withQueue bool, handler func(w http.ResponseWriter, r *http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	for key, value := range pathValues {
		req.SetPathValue(key, value)
	}
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", sessionUser)
	if withQueue {
		client, inspector, cleanup := setupAsynqTest(t)
		defer cleanup()
		ctx = context.WithValue(ctx, "asynq_client", client)
		ctx = context.WithValue(ctx, "asynq_inspector", inspector)
	}
	rr := httptest.NewRecorder()
	handler(rr, req.WithContext(ctx))
	return rr
}

func botRouteListedUUIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var page struct {
		Rows []BotDTO `json:"rows"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("failed to decode listed bots page: %v", err)
	}
	uuids := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		uuids = append(uuids, row.UUID)
	}
	return uuids
}

// ci#824: an automated (bot) session owns no runtime itself, so the canonical
// list route must resolve the acting user (the runtime owner) before scoping.
func TestBotsListResolvesActingUserForBotSession(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.bot.routes.list@example.com", false)
	stranger := createUserForBotsTest(t, DB, "stranger.bot.routes.list@example.com", false)

	_, leadUser := createBotRuntimeForTest(t, DB, owner, "lead-agent-list")
	worker, _ := createBotRuntimeForTest(t, DB, owner, "worker-agent-list")
	strangerRuntime, _ := createBotRuntimeForTest(t, DB, stranger, "stranger-agent-list")

	if !leadUser.IsAutomated {
		t.Fatalf("expected the lead session to be an automated bot user")
	}

	rr := botRouteRequest(t, DB, &leadUser, "GET", "/api/v1/bots/list", nil, nil, false, (&BotsHandler{}).List)
	if rr.Code != 200 {
		t.Fatalf("expected status 200 listing as a bot session, got %d: %s", rr.Code, rr.Body.String())
	}
	uuids := botRouteListedUUIDs(t, rr.Body.Bytes())
	if len(uuids) == 0 {
		t.Fatalf("expected a non-empty bot list for an automated session, got %s", rr.Body.String())
	}
	if !slices.Contains(uuids, worker.UUID) {
		t.Fatalf("expected the owner's worker runtime in the list, got %v", uuids)
	}
	if slices.Contains(uuids, strangerRuntime.UUID) {
		t.Fatalf("expected no stranger runtime in the list, got %v", uuids)
	}

	// A human session keeps its previous owner scope (no widening).
	humanRR := botRouteRequest(t, DB, stranger, "GET", "/api/v1/bots/list", nil, nil, false, (&BotsHandler{}).List)
	if humanRR.Code != 200 {
		t.Fatalf("expected status 200 listing as a human session, got %d: %s", humanRR.Code, humanRR.Body.String())
	}
	humanUUIDs := botRouteListedUUIDs(t, humanRR.Body.Bytes())
	if slices.Contains(humanUUIDs, worker.UUID) {
		t.Fatalf("expected a human session not to see another owner's runtime, got %v", humanUUIDs)
	}
	if !slices.Contains(humanUUIDs, strangerRuntime.UUID) {
		t.Fatalf("expected the human owner to see its own runtime, got %v", humanUUIDs)
	}
}

// ci#824: GET /bots/{identifier} must resolve the acting user for an automated
// session, by UUID and by owner-scoped name, without leaking another owner's bot.
func TestBotsGetResolvesActingUserForBotSession(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.bot.routes.get@example.com", false)
	stranger := createUserForBotsTest(t, DB, "stranger.bot.routes.get@example.com", false)

	_, leadUser := createBotRuntimeForTest(t, DB, owner, "lead-agent-get")
	worker, _ := createBotRuntimeForTest(t, DB, owner, "worker-agent-get")
	strangerRuntime, _ := createBotRuntimeForTest(t, DB, stranger, "stranger-agent-get")

	hr := &BotsHandler{}

	byUUID := botRouteRequest(t, DB, &leadUser, "GET", "/api/v1/bots/"+worker.UUID, map[string]string{"identifier": worker.UUID}, nil, false, hr.Get)
	if byUUID.Code != 200 {
		t.Fatalf("expected 200 fetching the owner's bot by uuid as a bot session, got %d: %s", byUUID.Code, byUUID.Body.String())
	}

	byName := botRouteRequest(t, DB, &leadUser, "GET", "/api/v1/bots/"+worker.Name, map[string]string{"identifier": worker.Name}, nil, false, hr.Get)
	if byName.Code != 200 {
		t.Fatalf("expected 200 fetching the owner's bot by name as a bot session, got %d: %s", byName.Code, byName.Body.String())
	}

	strangerRR := botRouteRequest(t, DB, &leadUser, "GET", "/api/v1/bots/"+strangerRuntime.UUID, map[string]string{"identifier": strangerRuntime.UUID}, nil, false, hr.Get)
	if strangerRR.Code != 404 {
		t.Fatalf("expected 404 fetching another owner's bot as a bot session, got %d: %s", strangerRR.Code, strangerRR.Body.String())
	}
}

// ci#824: POST /bots/{identifier}/interactions must resolve the acting user and
// create the interaction for that owner, not for the raw bot principal.
func TestBotsCreateInteractionResolvesActingUserForBotSession(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.bot.routes.interaction@example.com", false)

	_, leadUser := createBotRuntimeForTest(t, DB, owner, "lead-agent-interaction")
	worker, _ := createBotRuntimeForTest(t, DB, owner, "worker-agent-interaction")

	rr := botRouteRequest(t, DB, &leadUser, "POST", "/api/v1/bots/"+worker.UUID+"/interactions", map[string]string{"identifier": worker.UUID}, CreateBotInteractionRequest{
		Message: "work ci#824",
	}, true, (&BotsHandler{}).CreateInteraction)
	if rr.Code != 200 {
		t.Fatalf("expected status 200 creating an interaction as a bot session, got %d: %s", rr.Code, rr.Body.String())
	}

	var response BotInteractionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode interaction response: %v", err)
	}
	if response.ChatUUID == "" {
		t.Fatalf("expected chat_uuid in interaction response: %s", rr.Body.String())
	}

	var chat database.Chat
	if err := DB.Where("uuid = ?", response.ChatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected the interaction chat to exist: %v", err)
	}
	if chat.User1Id != owner.ID && chat.User2Id != owner.ID {
		t.Fatalf("expected the interaction to be owned by the resolved acting user, got user1=%d user2=%d", chat.User1Id, chat.User2Id)
	}
	if chat.User1Id == leadUser.ID || chat.User2Id == leadUser.ID {
		t.Fatalf("expected the bot principal not to own the interaction")
	}
}
