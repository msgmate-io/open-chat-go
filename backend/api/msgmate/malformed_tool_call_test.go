package msgmate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestTruncatedToolCallDoesNotEndTheTurn is the ci#1098 regression: a provider
// emission whose tool-call arguments are cut off mid-JSON used to produce an
// empty executed-call batch, which the loop treated as "no usable tool call"
// and ended the turn. The failed call must instead be fed back to the model
// with a correction instruction so the agent can re-issue it and continue.
func TestTruncatedToolCallDoesNotEndTheTurn(t *testing.T) {
	tool := &duplicateCallRecordingTool{BaseTool: &BaseTool{ToolName: "coder_read"}}
	toolMap := map[string]Tool{"coder_read": tool}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if requests.Add(1) == 1 {
			// Truncated JSON: the closing brace is missing.
			_, _ = io.WriteString(w, sseFromToolCallChunks([]map[string]interface{}{
				toolCallChunk("call-1", "coder_read", `{"path":"notes.md`),
			}))
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"recovered\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm",
		5, // tool_call_max_total
		5, // tool_call_max_failed
		[]map[string]interface{}{{"role": "user", "content": "read notes.md"}},
		nil, toolMap, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-1"},
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range chunks {
		}
	}()
	go func() {
		for range usage {
		}
	}()
	var got []ToolCall
	for call := range toolCalls {
		got = append(got, call)
	}
	err := <-errs
	<-done

	if err != nil {
		t.Fatalf("a truncated tool call must not end the turn, got error: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected a second provider round after the malformed call, got %d request(s)", got)
	}
	if tool.runs != 0 {
		t.Fatalf("the malformed call must not execute the tool, ran %d time(s)", tool.runs)
	}
	if len(got) != 1 {
		t.Fatalf("expected one failed tool-call event, got %+v", got)
	}
	if got[0].Status != ToolCallStatusFailed {
		t.Fatalf("expected the malformed call to be reported as failed, got %q", got[0].Status)
	}
	if !strings.Contains(got[0].Result, "not executed") || !strings.Contains(got[0].Result, "Re-issue") {
		t.Fatalf("the failed call must carry a correction instruction, got %q", got[0].Result)
	}
}
