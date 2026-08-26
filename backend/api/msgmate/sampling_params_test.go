package msgmate

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSamplingParamsFromConfig(t *testing.T) {
	t.Run("all values extracted from JSON numbers", func(t *testing.T) {
		config := map[string]interface{}{
			"temperature":       0.2,
			"max_tokens":        float64(2048),
			"top_p":             0.9,
			"presence_penalty":  0.1,
			"frequency_penalty": 0.3,
		}
		params := samplingParamsFromConfig(config)
		if params.Temperature == nil || *params.Temperature != 0.2 {
			t.Fatalf("expected temperature 0.2, got %v", params.Temperature)
		}
		if params.MaxTokens == nil || *params.MaxTokens != 2048 {
			t.Fatalf("expected max_tokens 2048, got %v", params.MaxTokens)
		}
		if params.TopP == nil || *params.TopP != 0.9 {
			t.Fatalf("expected top_p 0.9, got %v", params.TopP)
		}
		if params.PresencePenalty == nil || *params.PresencePenalty != 0.1 {
			t.Fatalf("expected presence_penalty 0.1, got %v", params.PresencePenalty)
		}
		if params.FrequencyPenalty == nil || *params.FrequencyPenalty != 0.3 {
			t.Fatalf("expected frequency_penalty 0.3, got %v", params.FrequencyPenalty)
		}
	})

	t.Run("zero values are set, not unset", func(t *testing.T) {
		config := map[string]interface{}{
			"temperature":       float64(0),
			"presence_penalty":  float64(0),
			"frequency_penalty": float64(0),
			"top_p":             float64(0),
		}
		params := samplingParamsFromConfig(config)
		if params.Temperature == nil || *params.Temperature != 0 {
			t.Fatalf("expected temperature pointer to 0, got %v", params.Temperature)
		}
		if params.PresencePenalty == nil || *params.PresencePenalty != 0 {
			t.Fatalf("expected presence_penalty pointer to 0, got %v", params.PresencePenalty)
		}
		if params.FrequencyPenalty == nil || *params.FrequencyPenalty != 0 {
			t.Fatalf("expected frequency_penalty pointer to 0, got %v", params.FrequencyPenalty)
		}
		if params.TopP == nil || *params.TopP != 0 {
			t.Fatalf("expected top_p pointer to 0, got %v", params.TopP)
		}
	})

	t.Run("missing keys stay nil", func(t *testing.T) {
		params := samplingParamsFromConfig(map[string]interface{}{"model": "some-model"})
		if !params.isEmpty() {
			t.Fatalf("expected empty sampling params, got %+v", params)
		}
	})

	t.Run("nil and invalid values stay nil", func(t *testing.T) {
		config := map[string]interface{}{
			"temperature":       nil,
			"max_tokens":        "not-a-number",
			"top_p":             math.NaN(),
			"presence_penalty":  map[string]interface{}{},
			"frequency_penalty": math.Inf(1),
		}
		params := samplingParamsFromConfig(config)
		if !params.isEmpty() {
			t.Fatalf("expected empty sampling params for invalid values, got %+v", params)
		}
	})

	t.Run("nil config map", func(t *testing.T) {
		if params := samplingParamsFromConfig(nil); !params.isEmpty() {
			t.Fatalf("expected empty sampling params for nil config, got %+v", params)
		}
	})
}

func TestApplySamplingParamsToRequestBody(t *testing.T) {
	temperature := 0.0
	maxTokens := 1024
	topP := 0.95
	presence := 0.5
	frequency := -0.5

	fullParams := SamplingParams{
		Temperature:      &temperature,
		MaxTokens:        &maxTokens,
		TopP:             &topP,
		PresencePenalty:  &presence,
		FrequencyPenalty: &frequency,
	}

	t.Run("openai-compatible backend forwards everything including zero temperature", func(t *testing.T) {
		body := map[string]interface{}{"model": "m", "stream": true}
		applySamplingParamsToRequestBody(body, fullParams, "litellm")

		if got, ok := body["temperature"].(float64); !ok || got != 0.0 {
			t.Fatalf("expected temperature 0 to be forwarded, got %v", body["temperature"])
		}
		if got, ok := body["max_tokens"].(int); !ok || got != 1024 {
			t.Fatalf("expected max_tokens 1024, got %v", body["max_tokens"])
		}
		if got, ok := body["top_p"].(float64); !ok || got != 0.95 {
			t.Fatalf("expected top_p 0.95, got %v", body["top_p"])
		}
		if got, ok := body["presence_penalty"].(float64); !ok || got != 0.5 {
			t.Fatalf("expected presence_penalty 0.5, got %v", body["presence_penalty"])
		}
		if got, ok := body["frequency_penalty"].(float64); !ok || got != -0.5 {
			t.Fatalf("expected frequency_penalty -0.5, got %v", body["frequency_penalty"])
		}
	})

	t.Run("anthropic backend drops unsupported penalties", func(t *testing.T) {
		body := map[string]interface{}{"model": "m", "stream": true}
		applySamplingParamsToRequestBody(body, fullParams, "anthropic")

		if _, ok := body["temperature"]; !ok {
			t.Fatalf("expected temperature to be forwarded for anthropic")
		}
		if _, ok := body["max_tokens"]; !ok {
			t.Fatalf("expected max_tokens to be forwarded for anthropic")
		}
		if _, ok := body["top_p"]; !ok {
			t.Fatalf("expected top_p to be forwarded for anthropic")
		}
		if _, ok := body["presence_penalty"]; ok {
			t.Fatalf("expected presence_penalty to be dropped for anthropic")
		}
		if _, ok := body["frequency_penalty"]; ok {
			t.Fatalf("expected frequency_penalty to be dropped for anthropic")
		}
	})

	t.Run("empty params add no keys", func(t *testing.T) {
		body := map[string]interface{}{"model": "m", "stream": true}
		applySamplingParamsToRequestBody(body, SamplingParams{}, "openai")
		for _, key := range []string{"temperature", "max_tokens", "top_p", "presence_penalty", "frequency_penalty"} {
			if _, ok := body[key]; ok {
				t.Fatalf("expected no %q key for empty params", key)
			}
		}
	})
}

func TestMapInt64OrDefaultToleratesJSONNumbers(t *testing.T) {
	config := map[string]interface{}{
		"context":              float64(20),
		"tool_call_max_total":  int64(7),
		"tool_call_max_failed": 2.5,
	}
	if got := mapInt64OrDefault(config, "context", 10); got != 20 {
		t.Fatalf("expected context 20 from float64 JSON number, got %d", got)
	}
	if got := mapInt64OrDefault(config, "tool_call_max_total", 12); got != 7 {
		t.Fatalf("expected tool_call_max_total 7, got %d", got)
	}
	if got := mapInt64OrDefault(config, "tool_call_max_failed", 3); got != 3 {
		t.Fatalf("expected default 3 for non-integer float, got %d", got)
	}
	if got := mapInt64OrDefault(config, "missing", 42); got != 42 {
		t.Fatalf("expected default 42 for missing key, got %d", got)
	}
}

func TestProcessStreamingRequestForwardsSamplingParams(t *testing.T) {
	var capturedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	temperature := 0.0
	maxTokens := 1024
	params := SamplingParams{
		Temperature:      &temperature,
		MaxTokens:        &maxTokens,
		PresencePenalty:  nil,
		FrequencyPenalty: nil,
	}

	chunkChan := make(chan string, 16)
	usageChan := make(chan *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}, 16)
	toolChan := make(chan ToolCall, 16)
	errChan := make(chan error, 1)

	messages := []map[string]interface{}{{"role": "user", "content": "hello"}}
	_, err := processStreamingRequest(
		server.URL, "some-model", "litellm", messages, nil, map[string]Tool{}, "test-key",
		map[string]string{}, chunkChan, usageChan, toolChan, errChan, params,
	)
	if err != nil {
		t.Fatalf("processStreamingRequest failed: %v", err)
	}

	if capturedBody == nil {
		t.Fatalf("expected captured request body")
	}
	if got, ok := capturedBody["temperature"].(float64); !ok || got != 0.0 {
		t.Fatalf("expected temperature 0 in request body, got %v", capturedBody["temperature"])
	}
	if got, ok := capturedBody["max_tokens"].(float64); !ok || got != 1024 {
		t.Fatalf("expected max_tokens 1024 in request body, got %v", capturedBody["max_tokens"])
	}
	if _, ok := capturedBody["presence_penalty"]; ok {
		t.Fatalf("expected no presence_penalty in request body")
	}

	content := strings.Builder{}
	for len(chunkChan) > 0 {
		content.WriteString(<-chunkChan)
	}
	if content.String() != "ok" {
		t.Fatalf("expected streamed content ok, got %q", content.String())
	}
}
