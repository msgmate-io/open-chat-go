package msgmate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStreamChatCompletionRetriesTransientPreStreamError verifies that a
// transient pre-stream provider failure is retried and the tool loop resumes
// (instead of ending with an error-only reply) once the provider recovers.
func TestStreamChatCompletionRetriesTransientPreStreamError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"temporarily overloaded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	var retryCalls atomic.Int32
	retryOpts := ProviderRetryOptions{
		Enabled:     true,
		MaxAttempts: 3,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  5 * time.Millisecond,
		OnRetryAttempt: func(attempt, maxAttempts int, retryErr error, retryAt time.Time) {
			retryCalls.Add(1)
		},
	}

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm", 4, 3,
		[]map[string]interface{}{{"role": "user", "content": "hi"}},
		nil, map[string]Tool{}, "test-key", nil, nil, nil, SamplingParams{}, retryOpts,
		providerRequestMeta{ChatUUID: "chat-1"},
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
		t.Fatalf("retry should recover the transient error, got: %v", err)
	}
	<-drained

	if got := requests.Load(); got != 2 {
		t.Fatalf("expected 2 provider requests (initial + one retry), got %d", got)
	}
	if got := retryCalls.Load(); got != 1 {
		t.Fatalf("expected 1 retry callback, got %d", got)
	}
	if content.String() != "ok" {
		t.Fatalf("expected the tool loop to resume and stream ok, got %q", content.String())
	}
}

// TestStreamChatCompletionSingleAttemptWhenDisabled locks in the explicit
// provider_retry: false opt-out: exactly one provider request, error surfaced.
func TestStreamChatCompletionSingleAttemptWhenDisabled(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream down"}}`)
	}))
	defer server.Close()

	retryOpts := providerRetryOptionsFromConfig(map[string]interface{}{"provider_retry": false})

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm", 4, 3,
		[]map[string]interface{}{{"role": "user", "content": "hi"}},
		nil, map[string]Tool{}, "test-key", nil, nil, nil, SamplingParams{}, retryOpts,
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
	if err := <-errs; err == nil {
		t.Fatal("expected the provider error to surface when retries are disabled")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected exactly 1 provider request when retries are disabled, got %d", got)
	}
}
