package bots

import (
	"backend/database"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// The agent-facing handlers (AgentGet/AgentContinue/AgentStop) read {chat_uuid}
// from the path and used to hand it straight to findManageableInteraction, which
// only short-circuits on an empty value, so malformed input reached the
// `WHERE uuid = ?` lookup. These tests pin the util.ParsePathUUID guard added in
// front of every call: malformed input is rejected with the same 404 the lookup
// produces for an unknown interaction, and a well-formed UUID still resolves.

func agentManagedHandlers() []struct {
	name string
	fn   func(*BotsHandler) http.HandlerFunc
} {
	return []struct {
		name string
		fn   func(*BotsHandler) http.HandlerFunc
	}{
		{"AgentGet", func(h *BotsHandler) http.HandlerFunc { return h.AgentGet }},
		{"AgentContinue", func(h *BotsHandler) http.HandlerFunc { return h.AgentContinue }},
		{"AgentStop", func(h *BotsHandler) http.HandlerFunc { return h.AgentStop }},
	}
}

func malformedChatUUIDs() []struct {
	name  string
	value string
} {
	return []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"not a uuid", "not-a-uuid"},
		{"path fragment", "12345/67890"},
		{"truncated uuid", "0f8fad5b-d9cb-469f-a165-7086772895"},
		{"sql fragment", "' OR 1=1 --"},
	}
}

func TestAgentManagedHandlersRejectMalformedChatUUID(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.uuid.malformed@example.com", false)

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	for _, handler := range agentManagedHandlers() {
		handler := handler
		for _, tc := range malformedChatUUIDs() {
			tc := tc
			t.Run(handler.name+"/"+tc.name, func(t *testing.T) {
				h := &BotsHandler{}
				rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/interaction", map[string]string{
					"chat_uuid": tc.value,
				}, map[string]string{"message": "nudge"}, client, inspector, handler.fn(h))

				if rr.Code != http.StatusNotFound {
					t.Fatalf("expected status 404 for chat_uuid %q, got %d: %s", tc.value, rr.Code, rr.Body.String())
				}
				if !strings.Contains(rr.Body.String(), "Interaction not found") {
					t.Fatalf("expected the interaction-not-found body for chat_uuid %q, got %q", tc.value, rr.Body.String())
				}
			})
		}
	}
}

// A well-formed but unknown UUID must reach the ownership-scoped lookup instead
// of being caught by the guard: that is the pre-existing 404 path, and it shows
// the guard only rejects malformed input.
func TestAgentManagedHandlersPassWellFormedChatUUIDToLookup(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.uuid.unknown@example.com", false)

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	for _, handler := range agentManagedHandlers() {
		handler := handler
		t.Run(handler.name, func(t *testing.T) {
			h := &BotsHandler{}
			rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/interaction", map[string]string{
				"chat_uuid": uuid.NewString(),
			}, map[string]string{"message": "nudge"}, client, inspector, handler.fn(h))

			if rr.Code != http.StatusNotFound {
				t.Fatalf("expected status 404 for an unknown chat UUID, got %d: %s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "Interaction not found") {
				t.Fatalf("expected the interaction-not-found body, got %q", rr.Body.String())
			}
		})
	}
}

// Positive control on a real interaction: the guard must not reject it, and
// because it canonicalizes the path value an uppercase or whitespace-padded
// spelling of the same UUID resolves to the same chat (stored UUIDs are
// lowercase and the lookup is a case-sensitive string match).
func TestAgentGetAcceptsNormalizedSpellingOfARealInteraction(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.uuid.normalized@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "normalized-spelling-bot")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	chatUUID := dispatchInteractionForUUIDTest(t, DB, owner, bot.UUID, client, inspector)

	for _, spelling := range []struct {
		name  string
		value string
	}{
		{"canonical", chatUUID},
		{"uppercase", strings.ToUpper(chatUUID)},
		{"surrounding whitespace", " " + chatUUID + " "},
	} {
		spelling := spelling
		t.Run(spelling.name, func(t *testing.T) {
			h := &BotsHandler{}
			rr := agentDispatchRequest(t, DB, owner, "GET", "/api/v1/agents/interaction", map[string]string{
				"chat_uuid": spelling.value,
			}, nil, client, inspector, h.AgentGet)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200 for chat_uuid %q, got %d: %s", spelling.value, rr.Code, rr.Body.String())
			}
			var body map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("failed to decode the agent view: %v", err)
			}
			row, ok := body["row"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected a row object in the agent view, got %#v", body)
			}
			if got, _ := row["chat_uuid"].(string); got != chatUUID {
				t.Fatalf("expected the view to report the canonical chat UUID %q, got %q", chatUUID, got)
			}
		})
	}
}

// dispatchInteractionForUUIDTest creates an owner-scoped interaction through the
// dispatch handler and returns its chat UUID.
func dispatchInteractionForUUIDTest(t *testing.T, DB *gorm.DB, owner *database.User, botUUID string, client *asynq.Client, inspector *asynq.Inspector) string {
	t.Helper()
	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:     botUUID,
		Message: "work on the uuid guard",
	}, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 dispatching the interaction, got %d: %s", rr.Code, rr.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode the dispatch response: %v", err)
	}
	chatUUID, _ := response["chat_uuid"].(string)
	if chatUUID == "" {
		t.Fatalf("expected chat_uuid in the dispatch response: %s", rr.Body.String())
	}
	return chatUUID
}
