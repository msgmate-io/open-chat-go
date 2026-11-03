package msgmate

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The prompt cache is the dominant cost lever for the long, tool-call-heavy
// msgmate-coder interactions: a consecutive turn re-sends the whole (growing)
// context, and the provider must serve the stable prefix from its context cache
// instead of billing ~15k prompt tokens afresh every round. These tests pin the
// three things that make that possible:
//
//   1. the provider's cache accounting is parsed and surfaced (so a regression
//      in cache hits is observable), and
//   2. the streaming request asks the provider for the usage chunk that carries
//      that accounting, and
//   3. the request body is append-only across consecutive turns — the message
//      prefix and the tools list are byte-stable, which is what a context cache
//      keys on.

func TestTokenUsageParsesPromptCacheDetails(t *testing.T) {
	raw := []byte(`{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,"prompt_tokens_details":{"cached_tokens":800}}`)
	var usage TokenUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatalf("failed to parse usage: %v", err)
	}
	if usage.PromptTokens != 1000 || usage.CompletionTokens != 50 || usage.TotalTokens != 1050 {
		t.Fatalf("unexpected base usage: %+v", usage)
	}
	if got := usage.CachedTokens(); got != 800 {
		t.Fatalf("CachedTokens = %d, want 800", got)
	}
	if ratio := usage.CacheHitRatio(); ratio < 0.799 || ratio > 0.801 {
		t.Fatalf("CacheHitRatio = %v, want ~0.8", ratio)
	}
}

func TestTokenUsageWithoutCacheDetails(t *testing.T) {
	var usage TokenUsage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":1,"total_tokens":101}`), &usage); err != nil {
		t.Fatalf("failed to parse usage: %v", err)
	}
	if usage.CachedTokens() != 0 {
		t.Fatalf("CachedTokens = %d, want 0", usage.CachedTokens())
	}
	if usage.CacheHitRatio() != 0 {
		t.Fatalf("CacheHitRatio = %v, want 0", usage.CacheHitRatio())
	}
}

func TestCacheHitRatioIsBounded(t *testing.T) {
	// A provider that over-reports (or a defensive caller) must never yield a
	// ratio outside [0,1].
	over := TokenUsage{PromptTokens: 100, PromptTokensDetails: PromptTokensDetails{CachedTokens: 500}}
	if ratio := over.CacheHitRatio(); ratio != 1 {
		t.Fatalf("over-reported ratio = %v, want 1", ratio)
	}
	negative := TokenUsage{PromptTokens: 100, PromptTokensDetails: PromptTokensDetails{CachedTokens: -5}}
	if ratio := negative.CacheHitRatio(); ratio != 0 {
		t.Fatalf("negative ratio = %v, want 0", ratio)
	}
	empty := TokenUsage{}
	if ratio := empty.CacheHitRatio(); ratio != 0 {
		t.Fatalf("empty-prompt ratio = %v, want 0", ratio)
	}
}

func TestStreamingRequestEnablesIncludeUsage(t *testing.T) {
	// Both OpenAI-compatible backends must request the final usage chunk; the
	// provider's cache-hit accounting only arrives with it.
	for _, backend := range []string{"openai", "openrouter"} {
		body := buildStreamingRequestBody(backend, "deepseek/deepseek-v4-flash-0731", nil, nil, SamplingParams{})
		if body["stream"] != true {
			t.Fatalf("%s: stream flag is not set", backend)
		}
		streamOptions, ok := body["stream_options"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: stream_options missing (cache accounting would not be returned)", backend)
		}
		if streamOptions["include_usage"] != true {
			t.Fatalf("%s: include_usage is not enabled: %v", backend, streamOptions)
		}
	}
}

// consecutiveTurnMessages models what streamChatCompletion appends as a
// tool-call turn proceeds: the assistant tool_calls message, then the tool
// result message. Each turn strictly appends to the previous list.
func consecutiveTurnMessages() (turn1, turn2 []map[string]interface{}) {
	system := map[string]interface{}{"role": "system", "content": "You are the msgmate coder."}
	user := map[string]interface{}{"role": "user", "content": "fix the failing test"}
	turn1 = []map[string]interface{}{system, user}

	assistantToolCall := map[string]interface{}{
		"role":    "assistant",
		"content": "",
		"tool_calls": []interface{}{
			map[string]interface{}{
				"id":       "call_1",
				"type":     "function",
				"function": map[string]interface{}{"name": "coder_bash", "arguments": `{"command":"go test ./..."}`},
			},
		},
	}
	toolResult := map[string]interface{}{
		"role":         "tool",
		"tool_call_id": "call_1",
		"content":      "ok",
	}
	turn2 = []map[string]interface{}{system, user, assistantToolCall, toolResult}
	return turn1, turn2
}

func coderToolSpec() []interface{} {
	return []interface{}{
		map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "coder_read", "description": "read"}},
		map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "coder_bash", "description": "bash"}},
		map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "coder_edit", "description": "edit"}},
	}
}

func messagesOf(t *testing.T, body map[string]interface{}) []map[string]interface{} {
	t.Helper()
	messages, ok := body["messages"].([]map[string]interface{})
	if !ok {
		t.Fatalf("request body has no typed messages slice: %T", body["messages"])
	}
	return messages
}

func TestStreamingRequestBodyMessagesPrefixIsStableAcrossTurns(t *testing.T) {
	turn1, turn2 := consecutiveTurnMessages()

	first := buildStreamingRequestBody("openrouter", "m", normalizeMessagesForBackend(turn1, "openrouter"), coderToolSpec(), SamplingParams{})
	second := buildStreamingRequestBody("openrouter", "m", normalizeMessagesForBackend(turn2, "openrouter"), coderToolSpec(), SamplingParams{})

	firstMessages := messagesOf(t, first)
	secondMessages := messagesOf(t, second)

	if len(firstMessages) >= len(secondMessages) {
		t.Fatalf("the second turn did not grow the message list (%d -> %d)", len(firstMessages), len(secondMessages))
	}
	for i := range firstMessages {
		if !reflect.DeepEqual(firstMessages[i], secondMessages[i]) {
			t.Fatalf("message %d changed between consecutive turns — the provider cache prefix would miss:\n first=%#v\nsecond=%#v", i, firstMessages[i], secondMessages[i])
		}
	}
}

func TestStreamingRequestBodyToolsAreStableAcrossTurns(t *testing.T) {
	turn1, turn2 := consecutiveTurnMessages()
	tools := coderToolSpec()

	first := buildStreamingRequestBody("openrouter", "m", normalizeMessagesForBackend(turn1, "openrouter"), tools, SamplingParams{})
	second := buildStreamingRequestBody("openrouter", "m", normalizeMessagesForBackend(turn2, "openrouter"), tools, SamplingParams{})

	if !reflect.DeepEqual(first["tools"], second["tools"]) {
		t.Fatalf("the tools list changed between consecutive turns — the cache prefix would miss")
	}
}

func TestNormalizeMessagesAppendOnlyPrefix(t *testing.T) {
	// normalizeMessagesForBackend must not reorder or rewrite the leading
	// messages when the list grows; only appending is cache-safe.
	turn1, turn2 := consecutiveTurnMessages()
	n1 := normalizeMessagesForBackend(turn1, "openrouter")
	n2 := normalizeMessagesForBackend(turn2, "openrouter")
	if len(n1) > len(n2) {
		t.Fatalf("normalization shrank the list: %d -> %d", len(n1), len(n2))
	}
	for i := range n1 {
		if !reflect.DeepEqual(n1[i], n2[i]) {
			t.Fatalf("normalized message %d changed as the list grew: %#v vs %#v", i, n1[i], n2[i])
		}
	}
}
