package bots

import (
	"backend/database"
	"encoding/json"
	"testing"
)

// TestCanonicalBotRoutesResolveActingUserForAutomatedSession pins ci#789: the
// canonical bot surface (GET /api/v1/bots/list, GET /api/v1/bots/{identifier},
// POST /api/v1/bots/{identifier}/interactions) must resolve the acting user for
// an automated (bot) session before doing owner-scoped bot resolution, exactly
// like POST /api/v1/agents/dispatch already does. Otherwise a workflow that
// authenticates with a bot token sees an empty fleet and get_bot 404s even
// though the bot exists.
func TestCanonicalBotRoutesResolveActingUserForAutomatedSession(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.canonical.bots@example.com", false)
	runtime, botUser := createBotRuntimeForTest(t, DB, owner, "canonical-worker")

	// A stranger's bot must stay invisible to the bot session: resolving the
	// acting user must not turn into a fleet-wide read.
	stranger := createUserForBotsTest(t, DB, "stranger.canonical.bots@example.com", false)
	strangerRuntime, _ := createBotRuntimeForTest(t, DB, stranger, "stranger-worker")

	if !botUser.IsAutomated {
		t.Fatalf("expected bot session user to be automated")
	}

	listRR := agentDispatchRequest(t, DB, &botUser, "GET", "/api/v1/bots/list", nil, nil, nil, nil, (&BotsHandler{}).List)
	if listRR.Code != 200 {
		t.Fatalf("list: expected status 200 for a bot session, got %d: %s", listRR.Code, listRR.Body.String())
	}
	var page ListedBotsPage
	if err := json.Unmarshal(listRR.Body.Bytes(), &page); err != nil {
		t.Fatalf("list: failed to decode response: %v", err)
	}
	if len(page.Rows) == 0 {
		t.Fatalf("list: expected the owner fleet to be visible to the bot session, got an empty page")
	}
	var sawOwn, sawStranger bool
	for _, row := range page.Rows {
		switch row.UUID {
		case runtime.UUID:
			sawOwn = true
		case strangerRuntime.UUID:
			sawStranger = true
		}
	}
	if !sawOwn {
		t.Fatalf("list: expected the bot's own runtime %q in the page, got %v", runtime.UUID, page.Rows)
	}
	if sawStranger {
		t.Fatalf("list: leaked another owner's runtime %q into the bot session page", strangerRuntime.UUID)
	}

	// get_bot resolves by UUID and by owner-scoped name; both are exercised by the
	// python client's get_bot before it falls back to /contacts/list.
	for _, identifier := range []string{runtime.UUID, runtime.Name} {
		getRR := agentDispatchRequest(t, DB, &botUser, "GET", "/api/v1/bots/"+identifier, map[string]string{"identifier": identifier}, nil, nil, nil, (&BotsHandler{}).Get)
		if getRR.Code != 200 {
			t.Fatalf("get %q: expected status 200 for a bot session, got %d: %s", identifier, getRR.Code, getRR.Body.String())
		}
		var dto BotDTO
		if err := json.Unmarshal(getRR.Body.Bytes(), &dto); err != nil {
			t.Fatalf("get %q: failed to decode response: %v", identifier, err)
		}
		if dto.UUID != runtime.UUID {
			t.Fatalf("get %q: expected runtime %q, got %q", identifier, runtime.UUID, dto.UUID)
		}
	}

	strangerGet := agentDispatchRequest(t, DB, &botUser, "GET", "/api/v1/bots/"+strangerRuntime.UUID, map[string]string{"identifier": strangerRuntime.UUID}, nil, nil, nil, (&BotsHandler{}).Get)
	if strangerGet.Code != 404 {
		t.Fatalf("get stranger: expected status 404, got %d: %s", strangerGet.Code, strangerGet.Body.String())
	}

	// The canonical interaction route is what the tagged workflow path POSTs to;
	// it must start the interaction for the owner, not 404 on the bot lookup.
	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	interactionRR := agentDispatchRequest(t, DB, &botUser, "POST", "/api/v1/bots/"+runtime.UUID+"/interactions",
		map[string]string{"identifier": runtime.UUID},
		CreateBotInteractionRequest{Message: "work ci#789"},
		client, inspector, (&BotsHandler{}).CreateInteraction)
	if interactionRR.Code != 200 {
		t.Fatalf("interaction: expected status 200 for a bot session, got %d: %s", interactionRR.Code, interactionRR.Body.String())
	}
	var interaction BotInteractionResponse
	if err := json.Unmarshal(interactionRR.Body.Bytes(), &interaction); err != nil {
		t.Fatalf("interaction: failed to decode response: %v", err)
	}
	if interaction.ChatUUID == "" {
		t.Fatalf("interaction: expected chat_uuid in response: %s", interactionRR.Body.String())
	}

	var chat database.Chat
	if err := DB.Where("uuid = ?", interaction.ChatUUID).First(&chat).Error; err != nil {
		t.Fatalf("interaction: expected interaction chat to exist: %v", err)
	}
	if chat.ChatType != "interaction" {
		t.Fatalf("interaction: expected interaction chat type, got %q", chat.ChatType)
	}
	if chat.User1Id != owner.ID && chat.User2Id != owner.ID {
		t.Fatalf("interaction: expected the bot owner to be a participant, got user1=%d user2=%d", chat.User1Id, chat.User2Id)
	}
}

// TestCanonicalBotRoutesKeepHumanSessionsOwnerScoped guards against the acting
// user resolution widening a human caller's visibility.
func TestCanonicalBotRoutesKeepHumanSessionsOwnerScoped(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.canonical.human@example.com", false)
	runtime, _ := createBotRuntimeForTest(t, DB, owner, "human-worker")
	stranger := createUserForBotsTest(t, DB, "stranger.canonical.human@example.com", false)
	strangerRuntime, _ := createBotRuntimeForTest(t, DB, stranger, "human-stranger")

	listRR := agentDispatchRequest(t, DB, owner, "GET", "/api/v1/bots/list", nil, nil, nil, nil, (&BotsHandler{}).List)
	if listRR.Code != 200 {
		t.Fatalf("list: expected status 200, got %d: %s", listRR.Code, listRR.Body.String())
	}
	var page ListedBotsPage
	if err := json.Unmarshal(listRR.Body.Bytes(), &page); err != nil {
		t.Fatalf("list: failed to decode response: %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].UUID != runtime.UUID {
		t.Fatalf("list: expected only the caller's runtime %q, got %v", runtime.UUID, page.Rows)
	}

	strangerGet := agentDispatchRequest(t, DB, owner, "GET", "/api/v1/bots/"+strangerRuntime.UUID, map[string]string{"identifier": strangerRuntime.UUID}, nil, nil, nil, (&BotsHandler{}).Get)
	if strangerGet.Code != 404 {
		t.Fatalf("get stranger: expected status 404, got %d: %s", strangerGet.Code, strangerGet.Body.String())
	}
}
