package msgmate

import "testing"

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
