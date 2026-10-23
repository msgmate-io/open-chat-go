package msgmate

import (
	"strings"
	"testing"
)

func TestInjectContextSummaryInsertsAfterSystemPrompt(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "base prompt"},
		{"role": "user", "content": "hello"},
	}
	out := injectContextSummary(messages, "  compacted summary  ")
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if role, _ := out[0]["role"].(string); role != "system" {
		t.Fatalf("first message must stay the base system prompt, got %q", role)
	}
	if role, _ := out[1]["role"].(string); role != "system" {
		t.Fatalf("second message must be the summary system message, got %q", role)
	}
	content, _ := out[1]["content"].(string)
	if !strings.Contains(content, "compacted summary") {
		t.Fatalf("summary content missing: %q", content)
	}
	if role, _ := out[2]["role"].(string); role != "user" {
		t.Fatalf("original user message must be preserved, got %q", role)
	}
	// The original slice must not be mutated in place.
	if len(messages) != 2 {
		t.Fatalf("input messages mutated: len=%d", len(messages))
	}
}

func TestInjectContextSummaryEmptyNoop(t *testing.T) {
	messages := []map[string]interface{}{{"role": "user", "content": "hi"}}
	out := injectContextSummary(messages, "   ")
	if len(out) != 1 {
		t.Fatalf("empty summary must be a no-op, got %d messages", len(out))
	}
}

func TestInjectContextSummaryWithoutSystemPrompt(t *testing.T) {
	messages := []map[string]interface{}{{"role": "user", "content": "hi"}}
	out := injectContextSummary(messages, "sum")
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if role, _ := out[0]["role"].(string); role != "system" {
		t.Fatalf("summary must lead when there is no system prompt, got %q", role)
	}
}

func TestTrimMessagesForContextRetryPreservesLeadingSystemMessages(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "base"},
		{"role": "system", "content": "summary"},
	}
	for i := 0; i < DefaultContextRetryTailMessages+5; i++ {
		messages = append(messages, map[string]interface{}{"role": "user", "content": "m"})
	}
	trimmed, ok := trimMessagesForContextRetry(messages)
	if !ok {
		t.Fatalf("expected trimming to happen")
	}
	if len(trimmed) > DefaultContextRetryTailMessages+2 {
		t.Fatalf("trimmed to %d messages, expected tail+system floor", len(trimmed))
	}
	if role, _ := trimmed[0]["role"].(string); role != "system" {
		t.Fatalf("base system prompt must survive, got %q", role)
	}
	if content, _ := trimmed[1]["content"].(string); content != "summary" {
		t.Fatalf("compacted summary system message must survive, got %q", content)
	}
}

func TestTrimMessagesForContextRetryNoLeadingSystem(t *testing.T) {
	messages := []map[string]interface{}{}
	for i := 0; i < DefaultContextRetryTailMessages+3; i++ {
		messages = append(messages, map[string]interface{}{"role": "user", "content": "m"})
	}
	trimmed, ok := trimMessagesForContextRetry(messages)
	if !ok {
		t.Fatalf("expected trimming to happen")
	}
	if len(trimmed) != DefaultContextRetryTailMessages {
		t.Fatalf("expected exactly %d tail messages, got %d", DefaultContextRetryTailMessages, len(trimmed))
	}
}
