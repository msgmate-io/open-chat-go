package msgmate

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// dsmlTestToken renders a DeepSeek DSML control token. The control tokens are
// wrapped in the fullwidth vertical line rune U+FF5C; the tests build them
// programmatically so the source never has to embed the raw private-use glyphs.
func dsmlTestToken(name string) string {
	return "\uFF5C" + "DSML" + "\uFF5C" + name + "\uFF5C"
}

// dsmlTestSSE builds an SSE stream whose single assistant delta carries the
// given inline content (no structured tool_calls).
func dsmlTestSSE(content string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{"content": content},
		}},
		"usage": map[string]interface{}{"prompt_tokens": 10, "completion_tokens": 9, "total_tokens": 19},
	})
	var b strings.Builder
	b.WriteString("data: ")
	b.Write(payload)
	b.WriteString("\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// dsmlTestContent assembles a well-formed DSML tool-call block for name with
// JSON arguments.
func dsmlTestContent(name, args string) string {
	parts := []string{
		dsmlTestToken("tool_calls_begin"),
		dsmlTestToken("tool_call_begin"),
		dsmlTestToken("tool_sep"),
		name,
		dsmlTestToken("parameter_begin"),
		args,
		dsmlTestToken("parameter_end"),
		dsmlTestToken("tool_call_end"),
		dsmlTestToken("tool_calls_end"),
	}
	return strings.Join(parts, "\n")
}

// TestProcessStreamingResponseReaderRecoversDSMLToolCall is the regression test
// for ci#428: when the model emits an inline DeepSeek DSML tool call instead of
// a structured tool_call, the parser must recover it into an executed tool call
// rather than storing the raw fragment as the assistant text and finishing.
func TestProcessStreamingResponseReaderRecoversDSMLToolCall(t *testing.T) {
	tool := &recordingNonConfirmableTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}

	sse := dsmlTestSSE(dsmlTestContent("coder_read", `{"path":"a.txt"}`))

	chunkChan := make(chan string, 8)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		toolMap,
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("DSML tool call should be recovered, got error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if !result.usedTool {
		t.Fatalf("expected the recovered DSML call to count as tool usage")
	}
	if result.toolName != "coder_read" {
		t.Fatalf("unexpected tool name: %s", result.toolName)
	}
	if result.status != ToolCallStatusSucceeded {
		t.Fatalf("expected %s status, got %s", ToolCallStatusSucceeded, result.status)
	}
	if result.aiResponse != "" {
		t.Fatalf("the raw DSML fragment must not be stored as assistant text, got %q", result.aiResponse)
	}
	if tool.runCount != 1 {
		t.Fatalf("expected the recovered tool to run once, got %d", tool.runCount)
	}

	close(chunkChan)
	close(toolChan)
	for chunk := range chunkChan {
		if containsDSMLMarker(chunk) {
			t.Fatalf("DSML fragments must not be streamed to the client, got %q", chunk)
		}
	}
	seen := 0
	for tc := range toolChan {
		seen++
		if tc.ToolName != "coder_read" {
			t.Fatalf("unexpected tool call surfaced: %+v", tc)
		}
	}
	if seen == 0 {
		t.Fatalf("expected the recovered tool call to be surfaced on the tool channel")
	}
}

// TestProcessStreamingResponseReaderReportsMalformedDSML guards the "fail the
// turn with a clear error" half of ci#428: a DSML block from which no tool call
// can be recovered must surface an error instead of silently finishing.
func TestProcessStreamingResponseReaderReportsMalformedDSML(t *testing.T) {
	tool := &recordingNonConfirmableTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}

	// Truncated fragment: control tokens but no function name / JSON payload.
	sse := dsmlTestSSE(dsmlTestToken("tool_calls_begin") + "\n" + dsmlTestToken("tool_call_begin"))

	chunkChan := make(chan string, 8)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

	_, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		toolMap,
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err == nil {
		t.Fatalf("expected a clear error for an unrecoverable DSML block")
	}
	if !strings.Contains(err.Error(), "DSML") {
		t.Fatalf("expected the error to mention DSML, got: %v", err)
	}
	if tool.runCount != 0 {
		t.Fatalf("no tool must run for a malformed block, got %d", tool.runCount)
	}
}

// TestStreamChatCompletionRetriesEmptyPostConfirmationContinuation covers the
// minimum fallback of ci#428: a post-confirmation continuation that yields an
// assistant message with neither text nor tool calls is retried once.
func TestStreamChatCompletionRetriesEmptyPostConfirmationContinuation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if requests.Add(1) == 1 {
			// Empty assistant message: no content, no tool calls.
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm", 4, 3,
		[]map[string]interface{}{{"role": "user", "content": "hi"}},
		nil, map[string]Tool{}, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-1", PostConfirmationContinuation: true},
	)

	var content strings.Builder
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for c := range chunks {
			content.WriteString(c)
		}
	}()
	for range usage {
	}
	for range toolCalls {
	}
	if err := <-errs; err != nil {
		t.Fatalf("empty continuation retry should recover, got: %v", err)
	}
	<-drained

	if got := requests.Load(); got != 2 {
		t.Fatalf("expected 2 provider requests (empty + one retry), got %d", got)
	}
	if content.String() != "ok" {
		t.Fatalf("expected the retry to stream ok, got %q", content.String())
	}
}

// TestStreamChatCompletionDoesNotRetryEmptyNormalTurn locks in that the
// empty-response retry is scoped to post-confirmation continuations; a normal
// turn returning no text and no tool calls finishes without a second request.
func TestStreamChatCompletionDoesNotRetryEmptyNormalTurn(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm", 4, 3,
		[]map[string]interface{}{{"role": "user", "content": "hi"}},
		nil, map[string]Tool{}, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-1"},
	)

	go func() {
		for range chunks {
		}
	}()
	go func() {
		for range usage {
		}
	}()
	go func() {
		for range toolCalls {
		}
	}()
	if err := <-errs; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("normal empty turn must not be retried, got %d requests", got)
	}
}
