package bots

import (
	"backend/api/chats"
	"backend/database"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

func agentDispatchRequest(t *testing.T, DB *gorm.DB, sessionUser *database.User, method, path string, pathValues map[string]string, body interface{}, client *asynq.Client, inspector *asynq.Inspector, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
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
	if client != nil {
		ctx = context.WithValue(ctx, "asynq_client", client)
	}
	if inspector != nil {
		ctx = context.WithValue(ctx, "asynq_inspector", inspector)
	}
	rr := httptest.NewRecorder()
	handler(rr, req.WithContext(ctx))
	return rr
}

func TestAgentDispatchCreatesOwnerScopedInteraction(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.dispatch@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "worker-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:          bot.UUID,
		Message:      "work ci#540",
		Tags:         []string{"repo:open-chat-go", "issue:540"},
		AutoContinue: true,
		Autonomous:   true,
	}, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode dispatch response: %v", err)
	}
	chatUUID, _ := response["chat_uuid"].(string)
	if chatUUID == "" {
		t.Fatalf("expected chat_uuid in dispatch response: %s", rr.Body.String())
	}

	var chat database.Chat
	if err := DB.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to exist: %v", err)
	}
	if chat.User1Id != owner.ID && chat.User2Id != owner.ID {
		t.Fatalf("expected owner to be a participant, got user1=%d user2=%d", chat.User1Id, chat.User2Id)
	}
	if chat.ChatType != "interaction" {
		t.Fatalf("expected interaction chat type, got %q", chat.ChatType)
	}
	if !containsString(chat.Tags, "repo:open-chat-go") || !containsString(chat.Tags, "issue:540") {
		t.Fatalf("expected dispatch tags on chat, got %v", chat.Tags)
	}

	config := map[string]interface{}{}
	if chat.SharedConfig == nil {
		t.Fatalf("expected shared config on interaction")
	}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err != nil {
		t.Fatalf("failed to decode shared config: %v", err)
	}
	dispatchMeta, ok := config["agent_dispatch"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected agent_dispatch meta in shared config, got %v", config)
	}
	if autoContinue, _ := dispatchMeta["auto_continue"].(bool); !autoContinue {
		t.Fatalf("expected auto_continue recorded, got %v", dispatchMeta)
	}
	if autonomous, _ := dispatchMeta["autonomous"].(bool); !autonomous {
		t.Fatalf("expected autonomous recorded, got %v", dispatchMeta)
	}
}

func TestAgentDispatchFromBotActsForOwner(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.lead@example.com", false)
	_, leadUser := createBotRuntimeForTest(t, DB, owner, "lead-agent")
	worker, _ := createBotRuntimeForTest(t, DB, owner, "worker-agent")

	if !leadUser.IsAutomated {
		t.Fatalf("expected lead bot user to be automated")
	}

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	rr := agentDispatchRequest(t, DB, &leadUser, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:     worker.UUID,
		Message: "delegate this task",
	}, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode dispatch response: %v", err)
	}
	if acting, _ := response["acting_user_uuid"].(string); acting != owner.UUID {
		t.Fatalf("expected acting user to be the owner, got %q", acting)
	}
	chatUUID, _ := response["chat_uuid"].(string)

	var chat database.Chat
	if err := DB.Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to exist: %v", err)
	}
	if chat.User1Id != owner.ID && chat.User2Id != owner.ID {
		t.Fatalf("expected the owner (not the lead bot) to own the dispatched interaction")
	}
	if chat.User1Id == leadUser.ID || chat.User2Id == leadUser.ID {
		t.Fatalf("expected the lead bot not to be a participant")
	}
}

func TestAgentListGetContinueAndStop(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.manage@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "managed-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	hr := &BotsHandler{}
	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:     bot.UUID,
		Message: "start",
		Tags:    []string{"repo:open-chat-go"},
	}, client, inspector, hr.AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var dispatch map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &dispatch)
	chatUUID, _ := dispatch["chat_uuid"].(string)

	// List as the owner.
	listRR := agentDispatchRequest(t, DB, owner, "GET", "/api/v1/agents/interactions", nil, nil, client, inspector, hr.AgentList)
	if listRR.Code != 200 {
		t.Fatalf("expected list status 200, got %d: %s", listRR.Code, listRR.Body.String())
	}
	var listBody struct {
		Rows []AgentInteractionView `json:"rows"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	found := false
	for _, row := range listBody.Rows {
		if row.ChatUUID == chatUUID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dispatched interaction in agent list, got %s", listRR.Body.String())
	}

	// Continue nudges the agent.
	continueRR := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/interactions/"+chatUUID+"/continue", map[string]string{"chat_uuid": chatUUID}, map[string]string{"message": "please continue"}, client, inspector, hr.AgentContinue)
	if continueRR.Code != 200 {
		t.Fatalf("expected continue status 200, got %d: %s", continueRR.Code, continueRR.Body.String())
	}
	var msgCount int64
	DB.Model(&database.Message{}).Where("chat_id IN (SELECT id FROM chats WHERE uuid = ?)", chatUUID).Count(&msgCount)
	if msgCount < 2 {
		t.Fatalf("expected the nudge message to be persisted, count=%d", msgCount)
	}

	// Get returns the interaction plus its recent transcript.
	getRR := agentDispatchRequest(t, DB, owner, "GET", "/api/v1/agents/interactions/"+chatUUID, map[string]string{"chat_uuid": chatUUID}, nil, client, inspector, hr.AgentGet)
	if getRR.Code != 200 {
		t.Fatalf("expected get status 200, got %d: %s", getRR.Code, getRR.Body.String())
	}
	var getBody struct {
		Row      AgentInteractionView     `json:"row"`
		Messages []AgentTranscriptMessage `json:"messages"`
	}
	if err := json.Unmarshal(getRR.Body.Bytes(), &getBody); err != nil {
		t.Fatalf("failed to decode get response: %v", err)
	}
	if getBody.Row.ChatUUID != chatUUID {
		t.Fatalf("expected get row for chat %s, got %s", chatUUID, getBody.Row.ChatUUID)
	}
	if len(getBody.Messages) < 2 {
		t.Fatalf("expected the transcript to include the start + nudge messages, got %d", len(getBody.Messages))
	}

	// Stop is idempotent and succeeds.
	stopRR := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/interactions/"+chatUUID+"/stop", map[string]string{"chat_uuid": chatUUID}, nil, client, inspector, hr.AgentStop)
	if stopRR.Code != 200 {
		t.Fatalf("expected stop status 200, got %d: %s", stopRR.Code, stopRR.Body.String())
	}
}

func TestAgentContinueResumesAutoContinue(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.resume@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "resume-agent")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()
	hr := &BotsHandler{}

	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:          bot.UUID,
		Message:      "start",
		AutoContinue: true,
	}, client, inspector, hr.AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected dispatch status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var dispatch map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &dispatch)
	chatUUID, _ := dispatch["chat_uuid"].(string)

	var chat database.Chat
	if err := DB.Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected chat: %v", err)
	}
	if err := chats.StopAutoContinueForChat(DB, chat.ID, "needs_human"); err != nil {
		t.Fatalf("stop loop: %v", err)
	}
	if status := chats.AutoContinueStatusForChat(DB, chat); !status.Enabled || status.StopReason != "needs_human" {
		t.Fatalf("expected a stopped loop, got %+v", status)
	}

	continueRR := agentDispatchRequest(t, DB, owner, "POST",
		"/api/v1/agents/interactions/"+chatUUID+"/continue",
		map[string]string{"chat_uuid": chatUUID},
		map[string]interface{}{"message": "keep going", "resume_auto_continue": true},
		client, inspector, hr.AgentContinue)
	if continueRR.Code != 200 {
		t.Fatalf("expected continue status 200, got %d: %s", continueRR.Code, continueRR.Body.String())
	}
	var continueBody map[string]interface{}
	_ = json.Unmarshal(continueRR.Body.Bytes(), &continueBody)
	if resumed, _ := continueBody["auto_continue_resumed"].(bool); !resumed {
		t.Fatalf("expected auto_continue_resumed=true, got %s", continueRR.Body.String())
	}
	if status := chats.AutoContinueStatusForChat(DB, chat); status.StopReason != "" {
		t.Fatalf("expected the loop to be resumed, got %+v", status)
	}
}

func TestAgentDispatchScopesManagementToCaller(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.agent.scope@example.com", false)
	_, leadUser := createBotRuntimeForTest(t, DB, owner, "scope-lead")
	workerRuntime, workerUser := createBotRuntimeForTest(t, DB, owner, "scope-worker")
	_, intruderUser := createBotRuntimeForTest(t, DB, owner, "scope-intruder")

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()
	hr := &BotsHandler{}

	// The lead bot dispatches the worker on the owner's behalf.
	rr := agentDispatchRequest(t, DB, &leadUser, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:              workerRuntime.UUID,
		Message:          "do the work",
		AutoContinue:     true,
		AutoContinueMax:  5,
		CompletionMarker: "<<DONE>>",
	}, client, inspector, hr.AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected dispatch status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var dispatch map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &dispatch); err != nil {
		t.Fatalf("failed to decode dispatch response: %v", err)
	}
	chatUUID, _ := dispatch["chat_uuid"].(string)
	if chatUUID == "" {
		t.Fatalf("expected chat_uuid in dispatch response: %s", rr.Body.String())
	}

	// The dispatcher identity and the attached auto-continue budget are recorded.
	var chat database.Chat
	if err := DB.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to exist: %v", err)
	}
	config := map[string]interface{}{}
	_ = json.Unmarshal(chat.SharedConfig.ConfigData, &config)
	meta, _ := config["agent_dispatch"].(map[string]interface{})
	if got, _ := meta["dispatched_by"].(string); got != leadUser.UUID {
		t.Fatalf("expected dispatched_by=%s, got %q", leadUser.UUID, got)
	}
	if got, _ := meta["auto_continue_max"].(float64); int(got) != 5 {
		t.Fatalf("expected auto_continue_max=5 recorded, got %v", meta["auto_continue_max"])
	}

	// The lead (dispatcher), the worker (participant) and the owner (human) may
	// read/steer it.
	for name, caller := range map[string]*database.User{
		"dispatcher": &leadUser,
		"worker":     &workerUser,
		"owner":      owner,
	} {
		getRR := agentDispatchRequest(t, DB, caller, "GET", "/api/v1/agents/interactions/"+chatUUID, map[string]string{"chat_uuid": chatUUID}, nil, client, inspector, hr.AgentGet)
		if getRR.Code != 200 {
			t.Fatalf("%s: expected get status 200, got %d: %s", name, getRR.Code, getRR.Body.String())
		}
	}

	// An unrelated bot acting for the same owner must not reach it.
	intruderGet := agentDispatchRequest(t, DB, &intruderUser, "GET", "/api/v1/agents/interactions/"+chatUUID, map[string]string{"chat_uuid": chatUUID}, nil, client, inspector, hr.AgentGet)
	if intruderGet.Code != 404 {
		t.Fatalf("intruder: expected get status 404, got %d: %s", intruderGet.Code, intruderGet.Body.String())
	}
	intruderStop := agentDispatchRequest(t, DB, &intruderUser, "POST", "/api/v1/agents/interactions/"+chatUUID+"/stop", map[string]string{"chat_uuid": chatUUID}, nil, client, inspector, hr.AgentStop)
	if intruderStop.Code != 404 {
		t.Fatalf("intruder: expected stop status 404, got %d: %s", intruderStop.Code, intruderStop.Body.String())
	}

	// The intruder's list must not include the interaction.
	listRR := agentDispatchRequest(t, DB, &intruderUser, "GET", "/api/v1/agents/interactions", nil, nil, client, inspector, hr.AgentList)
	if listRR.Code != 200 {
		t.Fatalf("intruder: expected list status 200, got %d: %s", listRR.Code, listRR.Body.String())
	}
	var listBody struct {
		Rows []AgentInteractionView `json:"rows"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("failed to decode intruder list: %v", err)
	}
	for _, row := range listBody.Rows {
		if row.ChatUUID == chatUUID {
			t.Fatalf("intruder list must not include the lead-dispatched chat %s", chatUUID)
		}
	}
}

func containsString(values database.StringSliceJSON, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// createBotRuntimeForTest persists a bot runtime config + automated bot user
// directly. The create-bot API leaves the bot user's username empty, so more than
// one API-created bot per database collides on the username unique index; tests
// that need two bots create them directly with distinct usernames.
func createBotRuntimeForTest(t *testing.T, DB *gorm.DB, owner *database.User, name string) (database.BotRuntimeConfig, database.User) {
	t.Helper()
	username := strings.ReplaceAll(name, "-", "_")
	botUser := database.User{
		Name:         name,
		Username:     username,
		Email:        username + "@bot.local",
		IsAutomated:  true,
		ContactToken: name + "-contact",
	}
	if err := DB.Create(&botUser).Error; err != nil {
		t.Fatalf("failed to create bot user %q: %v", name, err)
	}
	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         owner.ID,
		Name:                name,
		DefaultSharedConfig: json.RawMessage(`{"model":"qwen3-4b-instruct-2507_vllm","backend":"litellm","endpoint":"https://litellm.t1m.me/v1","system_prompt":"test"}`),
		IsActive:            true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create bot runtime %q: %v", name, err)
	}
	if err := database.EnsureBotRuntimeOwner(DB, runtime.ID, owner.ID); err != nil {
		t.Fatalf("failed to record bot owner %q: %v", name, err)
	}
	return runtime, botUser
}
