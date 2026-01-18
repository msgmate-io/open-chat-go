package msgmate

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseMCPAuthHeaders_NormalizesBearerTokenType(t *testing.T) {
	headers := parseMCPAuthHeaders(map[string]interface{}{
		"access_token": "abc123",
		"token_type":   "bearer",
	})
	if got := headers["Authorization"]; got != "Bearer abc123" {
		t.Fatalf("expected canonical bearer auth header, got %q", got)
	}
}

func TestParseMCPResponseBody_ParsesSSEDataLine(t *testing.T) {
	body := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"x\",\"result\":{\"tools\":[]}}\n\n")
	parsed, err := parseMCPResponseBody(body)
	if err != nil {
		t.Fatalf("expected SSE body to parse, got error: %v", err)
	}
	if parsed.ID != "x" {
		t.Fatalf("expected id x, got %v", parsed.ID)
	}
}

func TestParseMCPIntegrationConfig_AllowsHTTPS(t *testing.T) {
	parsed, err := parseMCPIntegrationConfig(map[string]interface{}{
		"url": "https://mcp.figma.com/mcp",
	})
	if err != nil {
		t.Fatalf("expected https config to parse, got error: %v", err)
	}
	if parsed.URL != "https://mcp.figma.com/mcp" {
		t.Fatalf("expected url to stay unchanged, got %q", parsed.URL)
	}
}

func TestParseMCPIntegrationConfig_AllowsLoopbackHTTP(t *testing.T) {
	parsed, err := parseMCPIntegrationConfig(map[string]interface{}{
		"url": "http://localhost:8931/mcp",
	})
	if err != nil {
		t.Fatalf("expected localhost http config to parse, got error: %v", err)
	}
	if parsed.URL != "http://localhost:8931/mcp" {
		t.Fatalf("expected url to stay unchanged, got %q", parsed.URL)
	}
}

func TestParseMCPIntegrationConfig_RewritesLoopbackWhenEnabled(t *testing.T) {
	parsed, err := parseMCPIntegrationConfig(map[string]interface{}{
		"url": "http://localhost:8931/mcp",
		"resolve_localhost_to_host_docker_internal": true,
	})
	if err != nil {
		t.Fatalf("expected localhost rewrite config to parse, got error: %v", err)
	}
	if parsed.URL != "http://host.docker.internal:8931/mcp" {
		t.Fatalf("expected rewritten host.docker.internal URL, got %q", parsed.URL)
	}
}

func TestParseMCPIntegrationConfig_RejectsPlainHTTPForRemoteHost(t *testing.T) {
	_, err := parseMCPIntegrationConfig(map[string]interface{}{
		"url": "http://example.com/mcp",
	})
	if err == nil {
		t.Fatalf("expected error for remote plain http URL")
	}
}

func TestParseMCPIntegrationConfig_AllowsPlainHTTPForPrivateIPLiteral(t *testing.T) {
	for _, rawURL := range []string{
		"http://192.168.1.10:8931/mcp",
		"http://10.0.0.5/mcp",
		"http://172.18.0.3:8931/mcp",
	} {
		parsed, err := parseMCPIntegrationConfig(map[string]interface{}{"url": rawURL})
		if err != nil {
			t.Fatalf("expected private network http config %q to parse, got error: %v", rawURL, err)
		}
		if parsed.URL != rawURL {
			t.Fatalf("expected url to stay unchanged for %q, got %q", rawURL, parsed.URL)
		}
	}
}

func TestParseMCPIntegrationConfig_RejectsPlainHTTPForUnresolvableHostname(t *testing.T) {
	if _, err := net.LookupHost("unresolvable.mcp-host.invalid"); err == nil {
		t.Skipf("environment resolves invalid hostnames, cannot test fail-closed path")
	}
	_, err := parseMCPIntegrationConfig(map[string]interface{}{
		"url": "http://unresolvable.mcp-host.invalid:8931/mcp",
	})
	if err == nil {
		t.Fatalf("expected unresolvable hostname to be rejected for plain http")
	}
}

func TestParseMCPIntegrationConfig_AllowsPlainHTTPForHostnameResolvingToPrivateIP(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("cannot determine hostname: %v", err)
	}
	ips, lookupErr := net.LookupHost(hostname)
	if lookupErr != nil || len(ips) == 0 {
		t.Skipf("hostname %q does not resolve in this environment: %v", hostname, lookupErr)
	}
	private := false
	for _, ipRaw := range ips {
		if ip := net.ParseIP(ipRaw); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			private = true
			break
		}
	}
	if !private {
		t.Skipf("hostname %q does not resolve to a private address (%v)", hostname, ips)
	}
	if _, err := parseMCPIntegrationConfig(map[string]interface{}{
		"url": "http://" + hostname + ":8931/mcp",
	}); err != nil {
		t.Fatalf("expected http config for hostname resolving to private IP to parse, got error: %v", err)
	}
}

func TestIsServerNotInitializedError(t *testing.T) {
	err := fmt.Errorf("mcp request failed: 400 Bad Request: {\"error\":{\"message\":\"Bad Request: Server not initialized\"}}")
	if !isServerNotInitializedError(err) {
		t.Fatalf("expected server-not-initialized error to be detected")
	}
}

func TestMCPDoRequest_NotificationAllowsEmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	config := mcpIntegrationConfig{Transport: "http", URL: server.URL, RequestTimeoutSeconds: 5}
	_, _, err := mcpDoRequest(config, map[string]interface{}{}, "notifications/initialized", map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("expected empty notification response to be accepted, got error: %v", err)
	}
}

func TestMCPFunctionName_SanitizesColons(t *testing.T) {
	cases := map[string]struct{ integration, remote, want string }{
		"plain":                {"opencode-mcp", "list_interactions", "mcp_opencode-mcp_list_interactions"},
		"colon in remote name": {"server", "ns:tool", "mcp_server_ns_tool"},
		"dot in remote name":   {"server", "tool.name", "mcp_server_tool_name"},
		"slashes and spaces":   {"a/b", "c d", "mcp_a_b_c_d"},
	}
	for name, tc := range cases {
		if got := mcpFunctionName(tc.integration, tc.remote); got != tc.want {
			t.Fatalf("%s: mcpFunctionName(%q, %q) = %q, want %q", name, tc.integration, tc.remote, got, tc.want)
		}
	}
}

func TestMCPToolDescription_ExposesRemoteAndCallableNames(t *testing.T) {
	description := mcpToolDescription(
		"List interactions.",
		"mcp:opencode-mcp:openchat_list_interactions",
		"opencode-mcp",
		"openchat_list_interactions",
		"mcp_opencode-mcp_openchat_list_interactions",
	)
	for _, want := range []string{
		"List interactions.",
		"mcp:opencode-mcp:openchat_list_interactions",
		"openchat_list_interactions",
		"mcp_opencode-mcp_openchat_list_interactions",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("description %q does not mention %q", description, want)
		}
	}
}

func TestNewMCPToolFromSnapshot_AdvertisesCallableFunctionName(t *testing.T) {
	const namespaced = "mcp:opencode-mcp:openchat_list_interactions"
	snapshot := map[string]interface{}{
		namespaced: map[string]interface{}{
			"name":             namespaced,
			"integration_name": "opencode-mcp",
			"remote_tool_name": "openchat_list_interactions",
			"description":      "List interactions.",
			"input_schema":     map[string]interface{}{"type": "object"},
			"config":           map[string]interface{}{"url": "https://mcp.example.com/mcp"},
			"auth_data":        map[string]interface{}{},
		},
	}

	tool, found, err := NewMCPToolFromSnapshot(namespaced, snapshot)
	if err != nil {
		t.Fatalf("NewMCPToolFromSnapshot: %v", err)
	}
	if !found || tool == nil {
		t.Fatalf("expected tool %q to be found", namespaced)
	}

	constructed, ok := tool.ConstructTool().(map[string]interface{})
	if !ok {
		t.Fatalf("ConstructTool did not return a map")
	}
	function, ok := constructed["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("ConstructTool missing function object")
	}
	if got := function["name"]; got != "mcp_opencode-mcp_openchat_list_interactions" {
		t.Fatalf("provider function name = %v, want sanitized name", got)
	}
	desc, _ := function["description"].(string)
	if !strings.Contains(desc, namespaced) {
		t.Fatalf("function description %q must surface the remote name %q", desc, namespaced)
	}
	if !strings.Contains(desc, "mcp_opencode-mcp_openchat_list_interactions") {
		t.Fatalf("function description %q must surface the callable name", desc)
	}
}
