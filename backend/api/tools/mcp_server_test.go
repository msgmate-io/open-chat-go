package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/database"
)

func TestMCPInitialize(t *testing.T) {
	h := &MCPServerHandler{}
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	resp := h.dispatch(nil, nil, []byte(initBody))
	if resp == nil {
		t.Fatal("initialize returned no response")
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	result, _ := resp.Result.(map[string]interface{})
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	if result["serverInfo"] == nil {
		t.Fatal("missing serverInfo")
	}
}

func TestMCPToolsList(t *testing.T) {
	h := &MCPServerHandler{}
	listBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	resp := h.dispatch(nil, nil, []byte(listBody))
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp)
	}
	result, _ := resp.Result.(map[string]interface{})
	tools, _ := result["tools"].([]mcpToolDefinition)
	if len(tools) != 5 {
		t.Fatalf("tools count = %d, want 5", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.InputSchema == nil {
			t.Fatalf("tool %s missing inputSchema", tool.Name)
		}
	}
	for _, want := range []string{
		"openchat_list_interactions",
		"openchat_search_interactions",
		"openchat_get_interaction",
		"openchat_list_interaction_messages",
		"openchat_search_interaction_messages",
	} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}

func TestMCPNotificationReturnsNoResponse(t *testing.T) {
	h := &MCPServerHandler{}
	if resp := h.dispatch(nil, nil, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); resp != nil {
		t.Fatalf("notification should not produce a response, got %+v", resp)
	}
}

func TestMCPUnknownMethodAndBadJSON(t *testing.T) {
	h := &MCPServerHandler{}
	if resp := h.dispatch(nil, nil, []byte(`{"jsonrpc":"2.0","id":1,"method":"does/not/exist"}`)); resp == nil || resp.Error == nil || resp.Error.Code != mcpErrMethodNotFound {
		t.Fatalf("unknown method should return method-not-found, got %+v", resp)
	}
	if resp := h.dispatch(nil, nil, []byte(`not json`)); resp == nil || resp.Error == nil || resp.Error.Code != mcpErrParse {
		t.Fatalf("bad json should return parse error, got %+v", resp)
	}
}

func TestMCPToolCatalogRejectsUnknownTool(t *testing.T) {
	if _, err := runInteractionTool(nil, nil, "nope", nil); err == nil {
		t.Fatal("expected unknown tool error")
	}
}

func TestMCPArgHelpers(t *testing.T) {
	args := map[string]interface{}{"limit": float64(500), "offset": "3", "repo": " open-chat-go "}
	if got := mcpArgInt(args, "limit", mcpDefaultLimit, mcpMaxLimit); got != mcpMaxLimit {
		t.Fatalf("limit = %d, want capped %d", got, mcpMaxLimit)
	}
	if got := mcpArgInt(args, "offset", 0, 0); got != 3 {
		t.Fatalf("offset = %d, want 3", got)
	}
	if got := mcpArgString(args, "repo"); got != "open-chat-go" {
		t.Fatalf("repo = %q, want trimmed", got)
	}
	if got := mcpArgInt(nil, "limit", mcpDefaultLimit, mcpMaxLimit); got != mcpDefaultLimit {
		t.Fatalf("nil args limit = %d, want default", got)
	}
}

func TestMCPUnauthorizedRequest(t *testing.T) {
	h := &MCPServerHandler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	h.HandleMCP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without an authenticated user", rec.Code)
	}
}

func TestInteractionStateDerivation(t *testing.T) {
	text := "done"
	finished := database.Message{DataType: "text", Text: &text}
	finished.Sender.IsAutomated = true
	running := database.Message{DataType: "streaming", Text: &text}
	running.Sender.IsAutomated = true
	human := database.Message{DataType: "text", Text: &text}

	if got := interactionState(database.Chat{}); got != "" {
		t.Fatalf("empty chat state = %q, want empty", got)
	}
	if got := interactionState(database.Chat{LatestMessage: &finished}); got != "finished" {
		t.Fatalf("finished state = %q", got)
	}
	if got := interactionState(database.Chat{LatestMessage: &running}); got != "running" {
		t.Fatalf("running state = %q", got)
	}
	if got := interactionState(database.Chat{LatestMessage: &human}); got != "waiting" {
		t.Fatalf("waiting state = %q", got)
	}
}

// Ensure the MCP tool input schemas are valid JSON-serializable objects.
func TestMCPToolSchemasMarshal(t *testing.T) {
	for _, tool := range mcpInteractionTools() {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("tool %s schema not serializable: %v", tool.Name, err)
		}
		if !strings.Contains(string(encoded), `"type":"object"`) {
			t.Fatalf("tool %s schema missing object type: %s", tool.Name, encoded)
		}
		// `required` must be a JSON array, never null: the MCP TypeScript SDK
		// rejects an explicit null and makes clients fail with
		// "Failed to get tools".
		if strings.Contains(string(encoded), `"required":null`) {
			t.Fatalf("tool %s schema has required:null: %s", tool.Name, encoded)
		}
		required, ok := tool.InputSchema["required"].([]string)
		if !ok {
			t.Fatalf("tool %s schema required is not a string array: %T", tool.Name, tool.InputSchema["required"])
		}
		if required == nil {
			t.Fatalf("tool %s schema required is nil, want an empty array", tool.Name)
		}
	}
}
