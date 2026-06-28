package msgmate

import (
	"backend/database"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDynamicRESTToolCanOverrideBaseURLFromInit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user/self" {
			t.Fatalf("expected /api/v1/user/self, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	openAPISource := `{
		"openapi":"3.0.3",
		"info":{"title":"OpenChat API","version":"1.0.0"},
		"servers":[{"url":"https://example.invalid"}],
		"paths":{
			"/api/v1/user/self":{
				"get":{
					"operationId":"getUserSelf",
					"responses":{"200":{"description":"ok"}}
				}
			}
		}
	}`

	safetyJSON, err := json.Marshal(map[string]interface{}{
		"allow_private_ips": true,
	})
	if err != nil {
		t.Fatalf("failed to marshal safety policy: %v", err)
	}

	row := database.DynamicRESTTool{
		Name:              "rest_get_user_self_base_url_override",
		Description:       "Calls backend user self endpoint with base URL override",
		OpenAPISourceType: "inline",
		OpenAPISource:     openAPISource,
		OperationID:       "getUserSelf",
		BaseURLSource:     "init",
		BaseURLInputName:  "api_host",
		SafetyPolicy:      safetyJSON,
	}

	def, err := BuildDynamicRESTToolDefinition(row)
	if err != nil {
		t.Fatalf("failed to build dynamic rest tool definition: %v", err)
	}
	tool := NewToolFromDefinition(def)
	tool.SetInitData(map[string]interface{}{"api_host": server.URL})

	result, err := tool.RunTool(map[string]interface{}{})
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if result != `{"ok":true}` {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestDynamicRESTToolCensorsConfiguredResponseFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"admin","token":"secret-token","user":{"id":"u-1","ssn":"111-22-3333"},"users":[{"id":1,"email":"one@example.com"},{"id":2,"email":"two@example.com"}]}`))
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
					"responses":{"200":{"description":"ok"}}
				}
			}
		}
	}`, server.URL)

	safetyJSON, err := json.Marshal(map[string]interface{}{
		"allow_private_ips":      true,
		"response_censor_paths": []string{"token", "user.ssn", "users.*.email"},
	})
	if err != nil {
		t.Fatalf("failed to marshal safety policy: %v", err)
	}

	row := database.DynamicRESTTool{
		Name:              "rest_get_user_self_censor_fields",
		Description:       "Calls endpoint and removes sensitive fields",
		OpenAPISourceType: "inline",
		OpenAPISource:     openAPISource,
		OperationID:       "getUserSelf",
		SafetyPolicy:      safetyJSON,
	}

	def, err := BuildDynamicRESTToolDefinition(row)
	if err != nil {
		t.Fatalf("failed to build dynamic rest tool definition: %v", err)
	}

	tool := NewToolFromDefinition(def)
	result, err := tool.RunTool(map[string]interface{}{})
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if strings.Contains(result, "secret-token") || strings.Contains(result, "111-22-3333") || strings.Contains(result, "@example.com") {
		t.Fatalf("expected sensitive fields to be removed, got %s", result)
	}
	if result != `{"name":"admin","user":{"id":"u-1"},"users":[{"id":1},{"id":2}]}` {
		t.Fatalf("unexpected censored result: %s", result)
	}
}

func TestDynamicRESTToolRejectsCensorOnNonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain text response"))
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
					"responses":{"200":{"description":"ok"}}
				}
			}
		}
	}`, server.URL)

	safetyJSON, err := json.Marshal(map[string]interface{}{
		"allow_private_ips":      true,
		"response_censor_paths": []string{"token"},
	})
	if err != nil {
		t.Fatalf("failed to marshal safety policy: %v", err)
	}

	row := database.DynamicRESTTool{
		Name:              "rest_get_user_self_non_json_censor",
		Description:       "Should fail when body is non-JSON and censor paths are configured",
		OpenAPISourceType: "inline",
		OpenAPISource:     openAPISource,
		OperationID:       "getUserSelf",
		SafetyPolicy:      safetyJSON,
	}

	def, err := BuildDynamicRESTToolDefinition(row)
	if err != nil {
		t.Fatalf("failed to build dynamic rest tool definition: %v", err)
	}

	tool := NewToolFromDefinition(def)
	_, err = tool.RunTool(map[string]interface{}{})
	if err == nil {
		t.Fatalf("expected tool call to fail for non-JSON response")
	}
	if !strings.Contains(err.Error(), "response_censor_paths requires JSON response body") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDynamicRESTToolRejectsInvalidCensorPath(t *testing.T) {
	openAPISource := `{
		"openapi":"3.0.3",
		"info":{"title":"OpenChat API","version":"1.0.0"},
		"servers":[{"url":"https://example.com"}],
		"paths":{
			"/api/v1/user/self":{
				"get":{
					"operationId":"getUserSelf",
					"responses":{"200":{"description":"ok"}}
				}
			}
		}
	}`

	safetyJSON, err := json.Marshal(map[string]interface{}{
		"response_censor_paths": []string{"users.*"},
	})
	if err != nil {
		t.Fatalf("failed to marshal safety policy: %v", err)
	}

	row := database.DynamicRESTTool{
		Name:              "rest_get_user_self_invalid_censor_path",
		Description:       "Should reject wildcard-only terminal segments",
		OpenAPISourceType: "inline",
		OpenAPISource:     openAPISource,
		OperationID:       "getUserSelf",
		SafetyPolicy:      safetyJSON,
	}

	_, err = BuildDynamicRESTToolDefinition(row)
	if err == nil {
		t.Fatalf("expected build to fail with invalid censor path")
	}
	if !strings.Contains(err.Error(), "cannot end with wildcard") {
		t.Fatalf("unexpected build error: %v", err)
	}
}
