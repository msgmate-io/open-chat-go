package bots

import (
	"backend/database"
	"encoding/json"
	"testing"

	toolinterface "github.com/msgmate-io/go-tool-interface/toolinterface"
)

// The real `coder_bash` / `coder_select_workspace` definitions are shipped by
// the coding integration at runtime. A test binary that does not carry that
// integration's generated wiring would otherwise reject these configs with
// `references unknown tool`, which has nothing to do with the chat-start hook
// under test. Registering stand-in definitions keeps this file hermetic; when
// the integration is compiled in, its own definitions already own the names
// (they are registered from an init of the imported package, ie before this
// init runs), so nothing is overridden.
func init() {
	registered := map[string]bool{}
	for _, def := range toolinterface.List() {
		registered[def.Name] = true
	}
	noop := func(_ interface{}, _ map[string]interface{}) (string, error) { return "", nil }

	if !registered["coder_bash"] {
		// RequiresInit stays false: the integration's coder_bash is accepted
		// without a tool_init payload.
		if err := toolinterface.Register(toolinterface.Definition{Name: "coder_bash", Run: noop}); err != nil {
			panic(err)
		}
	}
	if !registered["coder_select_workspace"] {
		// RequiresInit with no schema of its own: the chat-start hook is what
		// has to seed its tool_init, and the payload it derives must validate.
		if err := toolinterface.Register(toolinterface.Definition{
			Name:         "coder_select_workspace",
			RequiresInit: true,
			Run:          noop,
		}); err != nil {
			panic(err)
		}
	}
}

// ci#819 part B: a dispatch for a coder bot must seed tool_init at chat start
// so the agent never has to call coder_select_workspace itself.
func TestAgentDispatchSeedsCoderWorkspaceInit(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.precallinit@example.com", false)
	runtime, _ := createBotRuntimeForTest(t, DB, owner, "precall-coder")

	config := map[string]interface{}{
		"model":         "qwen3-4b-instruct-2507_vllm",
		"backend":       "litellm",
		"endpoint":      "https://litellm.t1m.me/v1",
		"system_prompt": "test",
		"tools":         []interface{}{"coder_bash", "coder_select_workspace"},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := DB.Model(&database.BotRuntimeConfig{}).Where("id = ?", runtime.ID).Update("default_shared_config", encoded).Error; err != nil {
		t.Fatalf("failed to update bot runtime config: %v", err)
	}

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:     runtime.UUID,
		Message: "work ci#819",
		Tags:    []string{"repo:msgmate-io/open-chat-go", "issue:819"},
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
	if chat.SharedConfig == nil {
		t.Fatalf("expected shared config on interaction")
	}
	stored := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &stored); err != nil {
		t.Fatalf("failed to decode shared config: %v", err)
	}

	init, ok := stored["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected seeded tool_init in shared config, got %v", stored)
	}
	seed, ok := init["coder_select_workspace"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected coder_select_workspace init seeded, got %v", init)
	}
	if repo, _ := seed["git_repo"].(string); repo != "msgmate-io/open-chat-go" {
		t.Fatalf("expected seeded repo from dispatch tag, got %v", seed)
	}
}

// A non-coder config (no init-requiring tool) is left untouched.
func TestAgentDispatchLeavesNonCoderConfigUnseeded(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.precallplain@example.com", false)
	runtime, _ := createBotRuntimeForTest(t, DB, owner, "precall-plain")

	config := map[string]interface{}{
		"model":         "qwen3-4b-instruct-2507_vllm",
		"backend":       "litellm",
		"endpoint":      "https://litellm.t1m.me/v1",
		"system_prompt": "test",
		"tools":         []interface{}{"coder_bash"},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := DB.Model(&database.BotRuntimeConfig{}).Where("id = ?", runtime.ID).Update("default_shared_config", encoded).Error; err != nil {
		t.Fatalf("failed to update bot runtime config: %v", err)
	}

	client, inspector, cleanup := setupAsynqTest(t)
	defer cleanup()

	rr := agentDispatchRequest(t, DB, owner, "POST", "/api/v1/agents/dispatch", nil, AgentDispatchRequest{
		Bot:     runtime.UUID,
		Message: "work ci#819",
		Tags:    []string{"repo:msgmate-io/open-chat-go", "issue:819"},
	}, client, inspector, (&BotsHandler{}).AgentDispatch)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode dispatch response: %v", err)
	}
	chatUUID, _ := response["chat_uuid"].(string)

	var chat database.Chat
	if err := DB.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to exist: %v", err)
	}
	stored := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &stored); err != nil {
		t.Fatalf("failed to decode shared config: %v", err)
	}
	if _, present := stored["tool_init"]; present {
		t.Fatalf("expected no tool_init for a non-coder config, got %v", stored["tool_init"])
	}
}
