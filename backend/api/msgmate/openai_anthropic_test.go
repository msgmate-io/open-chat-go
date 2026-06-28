package msgmate

import "testing"

func TestNormalizeMessagesForBackendAnthropicRemovesTrailingAssistant(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "prefill"},
	}

	normalized := normalizeMessagesForBackend(messages, "anthropic")
	if len(normalized) != 2 {
		t.Fatalf("expected 2 messages after normalization, got %d", len(normalized))
	}
	if role, _ := normalized[len(normalized)-1]["role"].(string); role != "user" {
		t.Fatalf("expected conversation to end with user role, got %q", role)
	}
}

func TestNormalizeMessagesForBackendAnthropicKeepsNonAssistantTail(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "sys"},
		{"role": "assistant", "content": "tool call"},
		{"role": "tool", "content": "{\"ok\":true}"},
	}

	normalized := normalizeMessagesForBackend(messages, "anthropic")
	if len(normalized) != len(messages) {
		t.Fatalf("expected message count to remain %d, got %d", len(messages), len(normalized))
	}
	if role, _ := normalized[len(normalized)-1]["role"].(string); role != "tool" {
		t.Fatalf("expected trailing role tool, got %q", role)
	}
}

func TestNormalizeMessagesForBackendNonAnthropicUnchanged(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "world"},
	}

	normalized := normalizeMessagesForBackend(messages, "openai")
	if len(normalized) != len(messages) {
		t.Fatalf("expected unchanged message count %d, got %d", len(messages), len(normalized))
	}
	if role, _ := normalized[len(normalized)-1]["role"].(string); role != "assistant" {
		t.Fatalf("expected trailing role assistant, got %q", role)
	}
}
