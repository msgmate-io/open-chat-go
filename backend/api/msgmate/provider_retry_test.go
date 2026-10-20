package msgmate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestClassifyGenerationFailure(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantReason  string
		wantSummary bool
	}{
		{
			name:       "nil is internal",
			err:        nil,
			wantReason: FailureReasonInternal,
		},
		{
			name:       "unclassified is internal",
			err:        fmt.Errorf("unexpected decoder failure"),
			wantReason: FailureReasonInternal,
		},
		{
			name:       "context window",
			err:        fmt.Errorf("error: maximum context length is 128000 tokens"),
			wantReason: FailureReasonContextWindow,
		},
		{
			// The model volunteered its own compaction bookkeeping instead of
			// an answer (ci#562): neither a provider fault nor an internal
			// error, so it gets its own reason and its own guidance.
			name:       "leaked compaction transcript",
			err:        errLeakedCompactionTranscript,
			wantReason: FailureReasonLeakedCompaction,
		},
		{
			name:       "wrapped leaked compaction transcript",
			err:        fmt.Errorf("turn failed: %w", errLeakedCompactionTranscript),
			wantReason: FailureReasonLeakedCompaction,
		},
		{
			name:        "provider http failure",
			err:         &ProviderRequestError{StatusCode: 429, Body: `{"error":{"message":"rate limited"}}`, Attempts: 2},
			wantReason:  FailureReasonProviderError,
			wantSummary: true,
		},
		{
			name:       "legacy provider status string",
			err:        fmt.Errorf("non-200 response: 503 gateway down"),
			wantReason: FailureReasonProviderError,
		},
		{
			name:       "transient network",
			err:        fmt.Errorf("request failed: Post \"https://x\": connection reset by peer"),
			wantReason: FailureReasonTransientNetwork,
		},
		{
			name:       "plain transient marker",
			err:        fmt.Errorf("broken pipe"),
			wantReason: FailureReasonTransientNetwork,
		},
		{
			name:       "unrecognised request failure is internal",
			err:        fmt.Errorf("request failed: unsupported protocol"),
			wantReason: FailureReasonInternal,
		},
		{
			name:       "turn deadline is a timeout, not internal",
			err:        context.DeadlineExceeded,
			wantReason: FailureReasonTimeout,
		},
		{
			name:       "wrapped turn deadline is a timeout",
			err:        fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
			wantReason: FailureReasonTimeout,
		},
		{
			name:       "user cancel is not classified as a timeout",
			err:        context.Canceled,
			wantReason: FailureReasonInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyGenerationFailure(tt.err)
			if got.Reason != tt.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if tt.wantSummary && got.ProviderSummary == "" {
				t.Fatalf("expected a provider summary for %v", tt.err)
			}
			if got.Reason == FailureReasonProviderError && got.ProviderAttempts < 1 {
				t.Fatalf("provider failures must report at least one attempt, got %d", got.ProviderAttempts)
			}
		})
	}
}

func TestTruncateFailureDetail(t *testing.T) {
	long := ""
	for i := 0; i < 400; i++ {
		long += "x"
	}
	got := TruncateFailureDetail(long)
	if len(got) >= len(long) || got == long {
		t.Fatalf("expected truncation, got %d chars", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if got := TruncateFailureDetail("  spaced  "); got != "spaced" {
		t.Fatalf("expected trimmed detail, got %q", got)
	}
}

// TestTruncateFailureDetailStaysValidUTF8 covers ci#680 on the user-facing
// failure path: provider errors quote request content (the place an oversized
// tool dump ends up), so a byte-offset cut must not leave a torn rune in the
// message shown to the user.
func TestTruncateFailureDetailStaysValidUTF8(t *testing.T) {
	for _, fill := range []string{"é", "→", "🧑"} {
		detail := strings.Repeat(fill, 400)
		got := TruncateFailureDetail(detail)
		if !utf8.ValidString(got) {
			t.Fatalf("detail must stay valid UTF-8, got %q", got)
		}
		if !strings.HasSuffix(got, "…") {
			t.Fatalf("expected the truncation marker, got %q", got)
		}
	}
	// A short non-ASCII detail must survive untouched.
	if got := TruncateFailureDetail("ok é →"); got != "ok é →" {
		t.Fatalf("short detail must be kept as-is, got %q", got)
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

func TestProviderRetryOptionsFromConfigDefaultsToEnabled(t *testing.T) {
	opts := providerRetryOptionsFromConfig(map[string]interface{}{})
	if !opts.Enabled {
		t.Fatal("provider_retry must default to enabled so one blip cannot dead-end an interaction")
	}
	if opts.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", opts.MaxAttempts)
	}
	if opts.BaseBackoff != 2*time.Second {
		t.Fatalf("BaseBackoff = %s, want 2s", opts.BaseBackoff)
	}
	if opts.MaxBackoff != 30*time.Second {
		t.Fatalf("MaxBackoff = %s, want 30s", opts.MaxBackoff)
	}
	if got := effectiveProviderRetryAttempts(opts); got != 3 {
		t.Fatalf("effective attempts = %d, want 3", got)
	}
}

func TestProviderRetryOptionsFromConfigHonorsExplicitDisable(t *testing.T) {
	opts := providerRetryOptionsFromConfig(map[string]interface{}{"provider_retry": false})
	if opts.Enabled {
		t.Fatal("explicit provider_retry: false must keep the single-attempt behavior")
	}
	if got := effectiveProviderRetryAttempts(opts); got != 1 {
		t.Fatalf("effective attempts = %d, want 1", got)
	}
}

func TestProviderRetryOptionsFromConfigOverrides(t *testing.T) {
	opts := providerRetryOptionsFromConfig(map[string]interface{}{
		"provider_retry":                true,
		"provider_retry_max":            5,
		"provider_retry_backoff_ms":     500,
		"provider_retry_backoff_max_ms": 4000,
	})
	if !opts.Enabled || opts.MaxAttempts != 5 {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if opts.BaseBackoff != 500*time.Millisecond || opts.MaxBackoff != 4*time.Second {
		t.Fatalf("unexpected backoff: base=%s max=%s", opts.BaseBackoff, opts.MaxBackoff)
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
