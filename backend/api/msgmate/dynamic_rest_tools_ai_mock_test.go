package msgmate

import (
	"bufio"
	"backend/database"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProcessStreamingResponseReader_ExecutesDynamicRESTToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/user/self" {
			t.Fatalf("expected /api/v1/user/self path, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer ai-mock-token" {
			t.Fatalf("expected Authorization header, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"u-123","name":"Alice"}`))
	}))
	defer server.Close()

	openAPISource := fmt.Sprintf(`{
		"openapi":"3.0.3",
		"info":{"title":"Mock API","version":"1.0.0"},
		"servers":[{"url":%q}],
		"paths":{
			"/api/v1/user/self":{
				"get":{
					"operationId":"getUserSelf",
					"parameters":[
						{"in":"header","name":"Authorization","required":true,"schema":{"type":"string"}}
					],
					"responses":{"200":{"description":"ok"}}
				}
			}
		}
	}`, server.URL)

	bindingsJSON, err := json.Marshal([]map[string]interface{}{
		{"input_name": "auth_header", "source": "init", "in": "header", "name": "Authorization"},
	})
	if err != nil {
		t.Fatalf("failed to marshal bindings: %v", err)
	}

	safetyJSON, err := json.Marshal(map[string]interface{}{"allow_private_ips": true})
	if err != nil {
		t.Fatalf("failed to marshal safety policy: %v", err)
	}

	def, err := BuildDynamicRESTToolDefinition(database.DynamicRESTTool{
		Name:              "rest_get_user_self_ai_mock",
		Description:       "Test-only dynamic rest tool",
		OpenAPISourceType: "inline",
		OpenAPISource:     openAPISource,
		OperationID:       "getUserSelf",
		ParamBindings:     bindingsJSON,
		SafetyPolicy:      safetyJSON,
	})
	if err != nil {
		t.Fatalf("failed to build tool definition: %v", err)
	}

	tool := NewToolFromDefinition(def)
	tool.SetInitData(map[string]interface{}{"auth_header": "Bearer ai-mock-token"})
	toolMap := map[string]Tool{"rest_get_user_self_ai_mock": tool}

	sse := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call-1","type":"function","function":{"name":"rest_get_user_self_ai_mock","arguments":"{}"}}]}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	chunkChan := make(chan string, 4)
	usageChan := make(chan *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}, 2)
	toolChan := make(chan ToolCall, 2)

	result, err := processStreamingResponseReader(
		bufio.NewReader(strings.NewReader(sse)),
		toolMap,
		map[string]bool{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("stream processing failed: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if !result.usedTool {
		t.Fatalf("expected tool usage in stream result")
	}
	if result.toolName != "rest_get_user_self_ai_mock" {
		t.Fatalf("unexpected tool name in result: %s", result.toolName)
	}

	select {
	case tc := <-toolChan:
		if tc.ToolName != "rest_get_user_self_ai_mock" {
			t.Fatalf("unexpected tool call name: %s", tc.ToolName)
		}
		if tc.Result != `{"uuid":"u-123","name":"Alice"}` {
			t.Fatalf("unexpected tool result: %s", tc.Result)
		}
	default:
		t.Fatalf("expected emitted tool call")
	}
}
