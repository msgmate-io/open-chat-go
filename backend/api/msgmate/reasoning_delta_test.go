package msgmate

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func reasoningFieldChunk(field, text string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{field: text},
		}},
	}
}

func contentFieldChunk(text string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{"content": text},
		}},
	}
}

// TestProcessStreamingResponseReaderCapturesProviderReasoning is the ci#852
// regression for the LLM client half: OpenRouter streams the chain of thought on
// a dedicated `delta.reasoning` (and the common `delta.reasoning_content` alias)
// field. Those deltas used to be dropped entirely. They must now be surfaced
// in-band (sentinel-tagged) in stream order, before the visible content, and must
// never leak into the accumulated assistant response text.
func TestProcessStreamingResponseReaderCapturesProviderReasoning(t *testing.T) {
	sse := sseFromToolCallChunks([]map[string]interface{}{
		reasoningFieldChunk("reasoning", "Let me think."),
		reasoningFieldChunk("reasoning", " First tool."),
		reasoningFieldChunk("reasoning_content", " Alias path."),
		contentFieldChunk("Hello"),
		contentFieldChunk(" world"),
	})

	chunkChan := make(chan string, 32)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		map[string]Tool{},
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("stream processing failed: %v", err)
	}

	var got []string
	for len(chunkChan) > 0 {
		got = append(got, <-chunkChan)
	}

	want := []string{
		reasoningChunkPrefix + "Let me think.",
		reasoningChunkPrefix + " First tool.",
		reasoningChunkPrefix + " Alias path.",
		"Hello",
		" world",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d chunks, got %d: %q", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}

	if result.aiResponse != "Hello world" {
		t.Fatalf("provider reasoning leaked into the assistant response: %q", result.aiResponse)
	}
	// The reasoning chunks must be the only sentinel-tagged entries; the visible
	// content must never carry the sentinel.
	for _, chunk := range got[3:] {
		if strings.HasPrefix(chunk, reasoningChunkPrefix) {
			t.Fatalf("visible content was tagged as reasoning: %q", chunk)
		}
	}
}

// TestProcessStreamingResponseReaderNoReasoningIsClean guards the other half:
// a provider stream with no reasoning field yields only the plain content
// chunks, so nothing regresses for non-reasoning models.
func TestProcessStreamingResponseReaderNoReasoningIsClean(t *testing.T) {
	sse := sseFromToolCallChunks([]map[string]interface{}{
		contentFieldChunk("plain "),
		contentFieldChunk("answer"),
	})

	chunkChan := make(chan string, 16)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 4)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		map[string]Tool{},
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("stream processing failed: %v", err)
	}

	var got []string
	for len(chunkChan) > 0 {
		got = append(got, <-chunkChan)
	}
	if len(got) != 2 || got[0] != "plain " || got[1] != "answer" {
		t.Fatalf("expected plain content chunks, got %q", got)
	}
	if result.aiResponse != "plain answer" {
		t.Fatalf("unexpected assistant response: %q", result.aiResponse)
	}
}
