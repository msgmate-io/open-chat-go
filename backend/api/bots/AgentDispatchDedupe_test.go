package bots

import (
	"encoding/json"
	"testing"
)

// TestDedupeKeyTagNormalization pins the stable tag a dispatch dedupe key is
// stored under (ci#625). The `lead:` prefix keeps lead identities greppable in
// lists, the value is lowercased/trimmed, and an empty key means "no dedupe".
func TestDedupeKeyTagNormalization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"open-chat-go-ci", "lead:open-chat-go-ci"},
		{"  Open-Chat-Go-CI  ", "lead:open-chat-go-ci"},
		{"lead:open-chat-go-ci", "lead:open-chat-go-ci"},
		{"LEAD:open-chat-go-ci", "lead:open-chat-go-ci"},
		{"open chat go ci", "lead:open-chat-go-ci"},
	}
	for _, tc := range cases {
		if got := dedupeKeyTag(tc.in); got != tc.want {
			t.Errorf("dedupeKeyTag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAgentDispatchDedupeKeyReturnsLiveInteraction is the regression test for the
// concurrent-duplicate-lead token burn: a second dispatch carrying the same
// dedupe_key while the first interaction is still live must return the existing
// interaction (deduplicated=true) instead of starting a second full agent, while
// force=true still starts a new one (ci#625).
func TestAgentDispatchDedupeKeyReturnsLiveInteraction(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.lead.dedupe@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "lead-dedupe-agent")

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
		Bot:       bot.UUID,
		Message:   "run the development loop",
		Tags:      []string{"repo:open-chat-go"},
		DedupeKey: "open-chat-go-ci",
	})
	firstChat, _ := first["chat_uuid"].(string)
	if firstChat == "" {
		t.Fatalf("expected the first dispatch to start an interaction, got %s", first)
	}
	if deduped, _ := first["deduplicated"].(bool); deduped {
		t.Fatalf("the first dispatch must not be deduplicated, got %s", first)
	}
	if key, _ := first["dedupe_key"].(string); key != "lead:open-chat-go-ci" {
		t.Fatalf("expected the normalized dedupe key in the response, got %q", key)
	}

	second := dispatch(AgentDispatchRequest{
		Bot:       bot.UUID,
		Message:   "run the development loop again",
		Tags:      []string{"repo:open-chat-go"},
		DedupeKey: "open-chat-go-ci",
	})
	if deduped, _ := second["deduplicated"].(bool); !deduped {
		t.Fatalf("expected the concurrent duplicate dispatch to be deduplicated, got %s", second)
	}
	if chatUUID, _ := second["chat_uuid"].(string); chatUUID != firstChat {
		t.Fatalf("expected the live interaction %s to be returned, got %q", firstChat, chatUUID)
	}

	// force=true deliberately starts a second agent.
	forced := dispatch(AgentDispatchRequest{
		Bot:       bot.UUID,
		Message:   "forced second lead",
		DedupeKey: "open-chat-go-ci",
		Force:     true,
	})
	if deduped, _ := forced["deduplicated"].(bool); deduped {
		t.Fatalf("expected force=true to bypass the dedupe guard, got %s", forced)
	}
	if chatUUID, _ := forced["chat_uuid"].(string); chatUUID == "" || chatUUID == firstChat {
		t.Fatalf("expected force=true to start a new interaction, got %q", chatUUID)
	}

	// An unrelated (empty) dedupe key keeps the plain dispatch behaviour.
	plain := dispatch(AgentDispatchRequest{Bot: bot.UUID, Message: "unrelated work"})
	if deduped, _ := plain["deduplicated"].(bool); deduped {
		t.Fatalf("a dispatch without a dedupe key must never be deduplicated, got %s", plain)
	}
}
