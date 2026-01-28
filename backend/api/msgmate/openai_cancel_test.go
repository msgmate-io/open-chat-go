package msgmate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestStreamChatCompletionAbortsProviderRequestOnCancel verifies that a stop
// cancels the in-flight provider request instead of letting it stream to
// completion. The server keeps the response open until the request context is
// cancelled (or the test releases it), so the client must observe a
// context.Canceled error promptly.
func TestStreamChatCompletionAbortsProviderRequestOnCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-release:
		case <-time.After(15 * time.Second):
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunks, usage, toolCalls, errs := streamChatCompletion(
		ctx,
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

	// Give the request time to reach the provider, then stop the response.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("expected the provider request to abort on cancel")
	}
}
