package msgmate

import (
	"backend/database"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDynamicRESTToolCanCallBackendMeEndpoint(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/user/self" {
			t.Fatalf("expected path /api/v1/user/self, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected Authorization header, got %q", got)
		}
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"admin","uuid":"u-1"}`))
	}))
	defer server.Close()

	openAPISource := fmt.Sprintf(`{
		"openapi":"3.0.3",
		"info":{"title":"OpenChat API","version":"1.0.0"},
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
		{
			"input_name": "auth_header",
			"source":     "init",
			"in":         "header",
			"name":       "Authorization",
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal bindings: %v", err)
	}

	safetyJSON, err := json.Marshal(map[string]interface{}{
		"allow_private_ips": true,
	})
	if err != nil {
		t.Fatalf("failed to marshal safety policy: %v", err)
	}

	row := database.DynamicRESTTool{
		Name:              "rest_get_user_self_test",
		Description:       "Calls backend user self endpoint",
		OpenAPISourceType: "inline",
		OpenAPISource:     openAPISource,
		OperationID:       "getUserSelf",
		ParamBindings:     bindingsJSON,
		SafetyPolicy:      safetyJSON,
	}

	def, err := BuildDynamicRESTToolDefinition(row)
	if err != nil {
		t.Fatalf("failed to build dynamic rest tool definition: %v", err)
	}
	if !def.RequiresInit {
		t.Fatalf("expected dynamic rest tool to require init")
	}

	tool := NewToolFromDefinition(def)
	tool.SetInitData(map[string]interface{}{"auth_header": "Bearer test-token"})

	result, err := tool.RunTool(map[string]interface{}{})
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if !called {
		t.Fatalf("expected HTTP endpoint to be called")
	}
	if result != `{"name":"admin","uuid":"u-1"}` {
		t.Fatalf("unexpected tool result: %s", result)
	}
}
