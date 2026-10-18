package bots

import (
	"encoding/json"
	"testing"

	"backend/database"
)

// TestAgentDispatchAutonomousPromotesCoderInteraction guards the core half of
// ci#506: a dispatched native coder agent must not park on a per-action
// confirmation nobody approves. The trusted dispatch surface records the
// workflow-equivalent options, and an explicit `autonomous: true` request is
// promoted into the chat's own `coder_interaction: autonomous` mode — the
// contract the coder integration reads to lift the gate — while keeping the
// dispatch bookkeeping intact.
func TestAgentDispatchAutonomousPromotesCoderInteraction(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.autonomous@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "autonomous-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:          bot.UUID,
		Message:      "run unattended ci#506",
		AutoContinue: true,
		Autonomous:   true,
	}, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var dispatch map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &dispatch); err != nil {
		t.Fatalf("failed to decode dispatch response: %v", err)
	}
	chatUUID, _ := dispatch["chat_uuid"].(string)
	if chatUUID == "" {
		t.Fatalf("expected chat_uuid in dispatch response: %s", rr.Body.String())
	}

	var chat database.Chat
	if err := DB.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to exist: %v", err)
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err != nil {
		t.Fatalf("failed to decode shared config: %v", err)
	}
	if got, _ := config["coder_interaction"].(string); got != "autonomous" {
		t.Fatalf("expected coder_interaction=autonomous on the chat config, got %v (%v)", config["coder_interaction"], config)
	}
	meta, _ := config["agent_dispatch"].(map[string]interface{})
	if autonomous, _ := meta["autonomous"].(bool); !autonomous {
		t.Fatalf("expected the dispatch bookkeeping to survive the promotion, got %v", meta)
	}

	// The agent-facing view reports the resolved policy and its source, so a
	// watching lead agent can tell the managed agent cannot stall.
	getRR := agentDispatchRequest(t, DB, owner, "GET", "/api/v1/agents/interactions/"+chatUUID,
		map[string]string{"chat_uuid": chatUUID}, nil, client, inspector, (&BotsHandler{}).AgentGet)
	if getRR.Code != 200 {
		t.Fatalf("expected get status 200, got %d: %s", getRR.Code, getRR.Body.String())
	}
	var getBody struct {
		Row AgentInteractionView `json:"row"`
	}
	if err := json.Unmarshal(getRR.Body.Bytes(), &getBody); err != nil {
		t.Fatalf("failed to decode get response: %v", err)
	}
	if !getBody.Row.Autonomous {
		t.Fatalf("expected the interaction view to report autonomous, got %+v", getBody.Row)
	}
	if getBody.Row.AutonomousSource != "coder_interaction" {
		t.Fatalf("autonomous_source = %q, want %q", getBody.Row.AutonomousSource, "coder_interaction")
	}
}

// TestAgentDispatchWithoutAutonomousKeepsConfirmationGate is the control: a
// dispatch that does *not* ask for autonomous mode must leave the interaction
// interactive, so the per-action confirmation gate an operator relies on is
// never widened implicitly.
func TestAgentDispatchWithoutAutonomousKeepsConfirmationGate(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.interactive@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "interactive-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:     bot.UUID,
		Message: "stay interactive",
	}, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var dispatch map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &dispatch)
	chatUUID, _ := dispatch["chat_uuid"].(string)

	var chat database.Chat
	if err := DB.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to exist: %v", err)
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err != nil {
		t.Fatalf("failed to decode shared config: %v", err)
	}
	if _, present := config["coder_interaction"]; present {
		t.Fatalf("a non-autonomous dispatch must not declare a coder interaction mode, got %v", config["coder_interaction"])
	}

	getRR := agentDispatchRequest(t, DB, owner, "GET", "/api/v1/agents/interactions/"+chatUUID,
		map[string]string{"chat_uuid": chatUUID}, nil, client, inspector, (&BotsHandler{}).AgentGet)
	if getRR.Code != 200 {
		t.Fatalf("expected get status 200, got %d: %s", getRR.Code, getRR.Body.String())
	}
	var getBody struct {
		Row AgentInteractionView `json:"row"`
	}
	if err := json.Unmarshal(getRR.Body.Bytes(), &getBody); err != nil {
		t.Fatalf("failed to decode get response: %v", err)
	}
	if getBody.Row.Autonomous {
		t.Fatalf("expected an interactive interaction, got %+v", getBody.Row)
	}
}
