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
