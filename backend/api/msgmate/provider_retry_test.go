package msgmate

import (
	"fmt"
	"testing"
	"time"
)

func TestProviderErrorInfoFromProviderRequestError(t *testing.T) {
	err := &ProviderRequestError{StatusCode: 429, Body: `{"error":{"message":"rate limited"}}`, Attempts: 3}
	code, body, attempts, ok := providerErrorInfo(err)
	if !ok || code != 429 || attempts != 3 || body != `{"error":{"message":"rate limited"}}` {
		t.Fatalf("unexpected parse: code=%d attempts=%d ok=%v body=%q", code, attempts, ok, body)
	}
	// Error string must keep the legacy format.
	want := "non-200 response: 429 {\"error\":{\"message\":\"rate limited\"}}"
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestProviderErrorInfoLegacyString(t *testing.T) {
	code, body, attempts, ok := providerErrorInfo(fmt.Errorf("non-200 response: 503 gateway down"))
	if !ok || code != 503 || body != "gateway down" || attempts != 1 {
		t.Fatalf("code=%d body=%q attempts=%d ok=%v", code, body, attempts, ok)
	}
}

func TestProviderErrorInfoOtherErrors(t *testing.T) {
	if _, _, _, ok := providerErrorInfo(fmt.Errorf("request failed: connection reset by peer")); ok {
		t.Fatal("network errors must not be classified as provider status errors")
	}
	if _, _, _, ok := providerErrorInfo(nil); ok {
		t.Fatal("nil must not be classified as provider status error")
	}
}

func TestIsRetryableProviderRequestError(t *testing.T) {
	retryable := []*ProviderRequestError{
		{StatusCode: 429}, {StatusCode: 500}, {StatusCode: 502},
		{StatusCode: 503}, {StatusCode: 504}, {StatusCode: 408},
	}
	for _, err := range retryable {
		if !isRetryableProviderRequestError(err) {
			t.Fatalf("status %d should be retryable", err.StatusCode)
		}
	}
	notRetryable := []*ProviderRequestError{
		{StatusCode: 400}, {StatusCode: 401}, {StatusCode: 403},
		{StatusCode: 404}, {StatusCode: 422},
	}
	for _, err := range notRetryable {
		if isRetryableProviderRequestError(err) {
			t.Fatalf("status %d must not be retryable", err.StatusCode)
		}
	}
	if isRetryableProviderRequestError(fmt.Errorf("request failed: Post: context canceled")) {
		t.Fatal("cancellation must not be retryable")
	}
	if isRetryableProviderRequestError(fmt.Errorf("request failed: unsupported protocol")) {
		t.Fatal("unknown request failures must not be retryable")
	}
	if !isRetryableProviderRequestError(fmt.Errorf("request failed: Post \"https://x\": connection reset by peer")) {
		t.Fatal("transient connect failures should be retryable")
	}
}

func TestIsPreStreamProviderFailure(t *testing.T) {
	if !isPreStreamProviderFailure(&ProviderRequestError{StatusCode: 429}) {
		t.Fatal("non-200 status errors are pre-stream failures")
	}
	if !isPreStreamProviderFailure(fmt.Errorf("request failed: connection reset by peer")) {
		t.Fatal("connect failures are pre-stream failures")
	}
	if isPreStreamProviderFailure(fmt.Errorf("stream interrupted: connection reset by peer")) {
		t.Fatal("mid-stream failures must not be retryable")
	}
}

func TestProviderRetryBackoffDelay(t *testing.T) {
	base := 2 * time.Second
	max := 8 * time.Second
	cases := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 8 * time.Second}, // capped
	}
	for _, c := range cases {
		if got := providerRetryBackoffDelay(c.attempt, base, max); got != c.expected {
			t.Fatalf("attempt %d: got %s, want %s", c.attempt, got, c.expected)
		}
	}
	// Zero base falls back to the default.
	if got := providerRetryBackoffDelay(1, 0, 0); got != 2*time.Second {
		t.Fatalf("default base: got %s", got)
	}
}

func TestEffectiveProviderRetryAttempts(t *testing.T) {
	if got := effectiveProviderRetryAttempts(ProviderRetryOptions{Enabled: false, MaxAttempts: 5}); got != 1 {
		t.Fatalf("disabled retry must be single attempt, got %d", got)
	}
	if got := effectiveProviderRetryAttempts(ProviderRetryOptions{Enabled: true, MaxAttempts: 3}); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
	if got := effectiveProviderRetryAttempts(ProviderRetryOptions{Enabled: true, MaxAttempts: 50}); got != maxProviderRetryAttempts {
		t.Fatalf("got %d, want cap %d", got, maxProviderRetryAttempts)
	}
	if got := effectiveProviderRetryAttempts(ProviderRetryOptions{Enabled: true, MaxAttempts: 0}); got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
}

func TestExtractProviderErrorDetailOpenRouter(t *testing.T) {
	body := `{"error":{"message":"nvidia/nemotron is temporarily rate-limited upstream","code":429,"metadata":{"provider_name":"CoreWeave","raw":"rate_limit_exceeded"}}}`
	summary, providerName := extractProviderErrorDetail(429, body)
	if providerName != "CoreWeave" {
		t.Fatalf("providerName = %q", providerName)
	}
	if summary == "" || !contains(summary, "429") || !contains(summary, "rate-limited") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestExtractProviderErrorDetailOpenAI(t *testing.T) {
	body := `{"error":{"message":"insufficient_quota","type":"insufficient_quota","code":429}}`
	summary, _ := extractProviderErrorDetail(429, body)
	if !contains(summary, "insufficient_quota") || !contains(summary, "429") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestExtractProviderErrorDetailNonJSON(t *testing.T) {
	summary, _ := extractProviderErrorDetail(502, "Bad Gateway")
	if summary == "" || !contains(summary, "502") {
		t.Fatalf("summary = %q", summary)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && (haystack == needle ||
		len(haystack) >= len(needle) && (haystack[:len(needle)] == needle ||
			indexOf(haystack, needle) >= 0))
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
