package msgmate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	wsapi "backend/api/websocket"
	"backend/database"

	client "github.com/msgmate-io/go-client-integration/goclient"
)

// hiveMindLookupTestToolName is a stand-in for the hive-mind integration's
// `hive_mind_lookup` tool (ci#449). It mirrors the real tool's contract: it
// reads api_host/session_id from its init and forwards the call to the backend
// API, failing with the same error when the trusted runtime is missing. It
// deliberately does NOT declare RequiresInit, so the test proves the core
// injects the trusted runtime for every tool a tool-backed bot runs, not only
// for tools that ship an init schema.
const hiveMindLookupTestToolName = "hive_mind_lookup_test"

func init() {
	registerToolConstructor(hiveMindLookupTestToolName, nil, func() Tool {
		return NewToolFromDefinition(ToolDefinition{
			Name:           hiveMindLookupTestToolName,
			Description:    "test-only hive mind lookup stand-in",
			RequiresInit:   false,
			InputType:      map[string]interface{}{},
			RequiredParams: []string{},
			Parameters:     map[string]interface{}{},
			RunFunction: func(_ interface{}, init map[string]interface{}) (string, error) {
				apiHost, _ := init["api_host"].(string)
				sessionID, _ := init["session_id"].(string)
				if strings.TrimSpace(apiHost) == "" || strings.TrimSpace(sessionID) == "" {
					return "", fmt.Errorf("missing api_host/session_id in tool init")
				}
				runtimeRaw, _ := init["_runtime"].(map[string]interface{})
				encoded, _ := json.Marshal(map[string]interface{}{
					"api_host":   apiHost,
					"session_id": sessionID,
					"chat_uuid":  runtimeRaw["chat_uuid"],
				})
				return string(encoded), nil
			},
		})
	})
}

// TestToolBackedBotHiveMindLookupReceivesTrustedRuntime reproduces ci#449: a
// `msgmate-coder-bot` chat lists the hive-mind tools in its config but writes
// no tool_init for them. The bot reply builds its client with the backend host
// and bot session (queue/tasks/bot_reply.go); setupTools must merge that trusted
// runtime into the hive-mind tool init so a lookup succeeds instead of failing
// with "missing api_host/session_id in tool init".
func TestToolBackedBotHiveMindLookupReceivesTrustedRuntime(t *testing.T) {
	ocClient := client.NewClient("http://backend.example:1984")
	ocClient.SetSessionId("bot-session-token")
	aih := NewAIHandler(&BotContext{
		Client:  ocClient,
		BotUser: database.User{Model: database.Model{UUID: "bot-uuid"}},
	})

	message := wsapi.NewMessage{Type: "new_message"}
	message.Content.ChatUUID = "chat-uuid"
	message.Content.SenderUUID = "sender-uuid"

	// syncCoderChatTools injects the hive-mind names into `tools`; it never
	// writes their tool_init.
	tools := []string{hiveMindLookupTestToolName}
	toolInit := map[string]interface{}{}

	_, toolMap, _, _ := aih.setupTools(message, tools, toolInit, map[string]interface{}{}, map[string]interface{}{})
	tool, ok := toolMap[hiveMindLookupTestToolName]
	if !ok {
		t.Fatalf("expected the hive-mind lookup tool to be registered, got %v", toolMap)
	}

	result, err := tool.RunTool(map[string]interface{}{"query": "msgmate coder"})
	if err != nil {
		t.Fatalf("hive-mind lookup failed: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unexpected lookup result %q: %v", result, err)
	}
	if payload["api_host"] != "http://backend.example:1984" {
		t.Fatalf("api_host = %v, want the bot client host", payload["api_host"])
	}
	if payload["session_id"] != "bot-session-token" {
		t.Fatalf("session_id = %v, want the bot client session", payload["session_id"])
	}
	if payload["chat_uuid"] != "chat-uuid" {
		t.Fatalf("chat_uuid = %v, want the trusted runtime chat scope", payload["chat_uuid"])
	}
}

// TestHiveMindLookupUsesExplicitToolInitHost ensures an explicit tool_init
// api_host still wins over the trusted self-host fallback while the trusted
// session is always attached.
func TestHiveMindLookupUsesExplicitToolInitHost(t *testing.T) {
	ocClient := client.NewClient("http://backend.example:1984")
	ocClient.SetSessionId("bot-session-token")
	aih := NewAIHandler(&BotContext{
		Client:  ocClient,
		BotUser: database.User{Model: database.Model{UUID: "bot-uuid"}},
	})

	message := wsapi.NewMessage{Type: "new_message"}
	message.Content.ChatUUID = "chat-uuid"
	message.Content.SenderUUID = "sender-uuid"

	toolInit := map[string]interface{}{
		hiveMindLookupTestToolName: map[string]interface{}{"api_host": "https://explicit.example/"},
	}
	_, toolMap, _, _ := aih.setupTools(message, []string{hiveMindLookupTestToolName}, toolInit, map[string]interface{}{}, map[string]interface{}{})
	tool := toolMap[hiveMindLookupTestToolName]
	if tool == nil {
		t.Fatal("expected the hive-mind lookup tool to be registered")
	}
	result, err := tool.RunTool(map[string]interface{}{"query": "x"})
	if err != nil {
		t.Fatalf("hive-mind lookup failed: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unexpected lookup result %q: %v", result, err)
	}
	if payload["api_host"] != "https://explicit.example/" {
		t.Fatalf("api_host = %v, want the explicit tool_init host", payload["api_host"])
	}
	if payload["session_id"] != "bot-session-token" {
		t.Fatalf("session_id = %v, want the trusted bot session", payload["session_id"])
	}
}
