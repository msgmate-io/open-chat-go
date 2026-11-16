package bots

import (
	"backend/database"
	"encoding/json"
	"testing"
)

// TestAgentDispatchIdempotencyFiresOnce pins the durable dispatch guarantee from
// ci#1047: a replayed delivery of the same trigger event - the same
// (trigger_id, idempotency_key) - starts exactly one agent, and every replay
// returns the interaction the first dispatch created with deduplicated=true.
// force=true does not bypass it: a replay is a replay.
func TestAgentDispatchIdempotencyFiresOnce(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.idempotency@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "idempotent-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()
	hr := &BotsHandler{}

	dispatch := func(req AgentDispatchRequest) map[string]interface{} {
		t.Helper()
		rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, req, client, inspector, hr.AgentDispatch)
		if rr.Code != 200 {
			t.Fatalf("expected dispatch status 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode dispatch response: %v", err)
		}
		return body
	}

	first := dispatch(AgentDispatchRequest{
		Bot:            bot.UUID,
		Message:        "handle issue:1047:opened",
		TriggerID:      "trigger-1047",
		IdempotencyKey: "issue:1047:opened",
	})
	firstChat, _ := first["chat_uuid"].(string)
	if firstChat == "" {
		t.Fatalf("expected the first dispatch to start an interaction, got %s", first)
	}
	if deduped, _ := first["deduplicated"].(bool); deduped {
		t.Fatalf("the first dispatch of an event must not be deduplicated, got %s", first)
	}

	// The replay - same trigger, same event - must not start a second agent.
	for i := 0; i < 2; i++ {
		replay := dispatch(AgentDispatchRequest{
			Bot:            bot.UUID,
			Message:        "handle issue:1047:opened (redelivered)",
			TriggerID:      "trigger-1047",
			IdempotencyKey: "issue:1047:opened",
		})
		if deduped, _ := replay["deduplicated"].(bool); !deduped {
			t.Fatalf("expected replay %d of the same event to be deduplicated, got %s", i+1, replay)
		}
		if chatUUID, _ := replay["chat_uuid"].(string); chatUUID != firstChat {
			t.Fatalf("expected the first interaction %s to be returned, got %q", firstChat, chatUUID)
		}
		if key, _ := replay["idempotency_key"].(string); key != "issue:1047:opened" {
			t.Fatalf("expected the idempotency key to be echoed, got %q", key)
		}
	}

	// force=true starts a new interaction only when the trigger event is new: it
	// deliberately does NOT pierce the durable idempotency guard.
	forced := dispatch(AgentDispatchRequest{
		Bot:            bot.UUID,
		Message:        "forced replay",
		TriggerID:      "trigger-1047",
		IdempotencyKey: "issue:1047:opened",
		Force:          true,
	})
	if deduped, _ := forced["deduplicated"].(bool); !deduped {
		t.Fatalf("expected force=true to still dedupe a replayed event, got %s", forced)
	}

	// An empty idempotency key keeps the pre-ci#1047 behaviour: always dispatch.
	for i := 0; i < 2; i++ {
		plain := dispatch(AgentDispatchRequest{Bot: bot.UUID, Message: "no idempotency key here"})
		if deduped, _ := plain["deduplicated"].(bool); deduped {
			t.Fatalf("a dispatch without an idempotency key must never be deduplicated, got %s", plain)
		}
		if chatUUID, _ := plain["chat_uuid"].(string); chatUUID == "" || chatUUID == firstChat {
			t.Fatalf("expected a fresh interaction, got %q", chatUUID)
		}
	}

	var claims int64
	if err := DB.Model(&database.EventClaim{}).
		Where("scope = ? AND trigger_id = ? AND event_key = ?", database.EventClaimScopeAgentDispatch, "trigger-1047", "issue:1047:opened").
		Count(&claims).Error; err != nil {
		t.Fatalf("failed to count event claims: %v", err)
	}
	if claims != 1 {
		t.Fatalf("expected exactly one durable claim row for the replayed event, got %d", claims)
	}
}

// TestAgentDispatchIdempotencyDistinctTriggersBothFire pins the composite event
// identity at the HTTP layer: the same idempotency key under a different
// trigger_id is a different event and must dispatch, and so is a different key
// under the same trigger.
func TestAgentDispatchIdempotencyDistinctTriggersBothFire(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.idempotency.composite@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "composite-idempotency-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()
	hr := &BotsHandler{}

	dispatch := func(triggerID, key string) map[string]interface{} {
		t.Helper()
		rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
			Bot:            bot.UUID,
			Message:        "handle " + key,
			TriggerID:      triggerID,
			IdempotencyKey: key,
		}, client, inspector, hr.AgentDispatch)
		if rr.Code != 200 {
			t.Fatalf("expected dispatch status 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode dispatch response: %v", err)
		}
		return body
	}

	a := dispatch("trigger-a", "issue:1047:opened")
	b := dispatch("trigger-b", "issue:1047:opened")
	c := dispatch("trigger-a", "issue:1047:closed")

	for name, body := range map[string]map[string]interface{}{"trigger-a/key1": a, "trigger-b/key1": b, "trigger-a/key2": c} {
		if deduped, _ := body["deduplicated"].(bool); deduped {
			t.Fatalf("expected %s to be a distinct event and dispatch, got %s", name, body)
		}
		if chatUUID, _ := body["chat_uuid"].(string); chatUUID == "" {
			t.Fatalf("expected %s to start an interaction, got %s", name, body)
		}
	}

	chats := map[string]bool{}
	for _, body := range []map[string]interface{}{a, b, c} {
		chatUUID, _ := body["chat_uuid"].(string)
		if chats[chatUUID] {
			t.Fatalf("expected three distinct interactions, got %q twice", chatUUID)
		}
		chats[chatUUID] = true
	}
}

// TestAgentDispatchIdempotencySurvivesRestart pins the reason the claim lives in
// the database: the live-interaction dedupe only sees interactions that still
// exist, so it cannot stop a redelivery once the interaction is gone (process
// restart, retention). The durable claim must still dedupe the event and hand
// back the interaction the event was fulfilled with.
func TestAgentDispatchIdempotencySurvivesRestart(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.idempotency.restart@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "restart-idempotency-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	req := AgentDispatchRequest{
		Bot:            bot.UUID,
		Message:        "handle issue:1047:reopened",
		TriggerID:      "trigger-1047",
		IdempotencyKey: "issue:1047:reopened",
	}

	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, req, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected dispatch status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var first map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatalf("failed to decode dispatch response: %v", err)
	}
	firstChat, _ := first["chat_uuid"].(string)
	if firstChat == "" {
		t.Fatalf("expected the first dispatch to start an interaction, got %s", first)
	}

	// Simulate the restart: the in-memory/live view of the interaction is gone,
	// so nothing but the durable claim can remember that the event fired.
	if err := DB.Unscoped().Where("uuid = ?", firstChat).Delete(&database.Chat{}).Error; err != nil {
		t.Fatalf("failed to remove the interaction for the restart simulation: %v", err)
	}

	// A freshly constructed handler stands in for the next process.
	rr = agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, req, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected the post-restart dispatch status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var replay map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &replay); err != nil {
		t.Fatalf("failed to decode dispatch response: %v", err)
	}
	if deduped, _ := replay["deduplicated"].(bool); !deduped {
		t.Fatalf("expected the redelivered event to be deduplicated after a restart, got %s", replay)
	}
	if chatUUID, _ := replay["chat_uuid"].(string); chatUUID != firstChat {
		t.Fatalf("expected the stored interaction %s to be returned, got %q", firstChat, chatUUID)
	}

	var claims int64
	if err := DB.Model(&database.EventClaim{}).
		Where("scope = ? AND trigger_id = ? AND event_key = ?", database.EventClaimScopeAgentDispatch, "trigger-1047", "issue:1047:reopened").
		Count(&claims).Error; err != nil {
		t.Fatalf("failed to count event claims: %v", err)
	}
	if claims != 1 {
		t.Fatalf("expected exactly one durable claim row, got %d", claims)
	}
}
