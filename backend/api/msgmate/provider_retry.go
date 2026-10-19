package msgmate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	client "github.com/msgmate-io/go-client-integration/goclient"
)

// Cap on provider retry attempts so a misconfigured chat can never spin an
// interaction forever; chat-level provider_retry_max is clamped to this.
const maxProviderRetryAttempts = 8

// ProviderRetryOptions configures automatic retries of pre-stream provider
// request failures. Retries are enabled by default (at least one retry) so a
// single transient provider blip cannot dead-end an interaction; a chat can
// still opt out explicitly with provider_retry: false.
type ProviderRetryOptions struct {
	Enabled     bool
	MaxAttempts int           // total attempts including the first one
	BaseBackoff time.Duration // delay after the first failed attempt
	MaxBackoff  time.Duration // cap for the exponential growth
	// OnRetryAttempt is invoked after every failed attempt that will be
	// retried (used to post the provider-retry widget message to the chat).
	// It may be nil.
	OnRetryAttempt func(attempt int, maxAttempts int, retryErr error, retryAt time.Time)
}

// ProviderRequestError carries a failed provider HTTP response. Its Error()
// keeps the legacy "non-200 response: <code> <body>" format so existing
// string-based error matching (e.g. context-window detection, queue task
// failure classification) continues to work.
type ProviderRequestError struct {
	StatusCode int
	Body       string
	// Attempts is how many provider requests were made before giving up
	// (1 when retries are disabled or the first attempt failed fatally).
	Attempts int
}

func (e *ProviderRequestError) Error() string {
	return fmt.Sprintf("non-200 response: %d %s", e.StatusCode, e.Body)
}

// providerErrorInfo extracts the provider HTTP status, response body and the
// number of attempts that were made from a provider request error.
func providerErrorInfo(err error) (code int, body string, attempts int, ok bool) {
	if err == nil {
		return 0, "", 0, false
	}
	var pre *ProviderRequestError
	if errors.As(err, &pre) {
		return pre.StatusCode, pre.Body, pre.Attempts, true
	}
	// Legacy string form (tests and older error paths).
	const legacyPrefix = "non-200 response: "
	s := err.Error()
	if !strings.HasPrefix(s, legacyPrefix) {
		return 0, "", 0, false
	}
	rest := strings.TrimPrefix(s, legacyPrefix)
	codePart := rest
	if idx := strings.IndexByte(rest, ' '); idx >= 0 {
		codePart = rest[:idx]
		rest = rest[idx+1:]
	} else {
		rest = ""
	}
	parsedCode, convErr := strconv.Atoi(codePart)
	if convErr != nil {
		return 0, "", 0, false
	}
	return parsedCode, rest, 1, true
}

// retryableProviderStatuses lists the HTTP status codes that are worth
// retrying: transient provider-side problems (rate limits, overload, gateway
// hiccups) and request timeouts.
func isRetryableProviderStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// transientNetworkErrorMarkers are connection-level failures that are worth
// one more attempt (the provider host was momentarily unreachable).
var transientNetworkErrorMarkers = []string{
	"connection reset by peer",
	"broken pipe",
	"unexpected eof",
	"connection aborted",
	"tls handshake timeout",
	"connection timed out",
	"server closed idle connection",
	"proxyconnect tcp",
}

// isPreStreamProviderFailure reports whether err was produced while the
// provider request was being established (HTTP status check or connect), i.e.
// before any response content was streamed. Only such failures may be
// retried: a mid-stream drop must never be retried, because the already
// streamed partial output would be duplicated.
func isPreStreamProviderFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "non-200 response: ") {
		return true
	}
	return strings.Contains(msg, "request failed: ")
}

// isRetryableProviderRequestError reports whether the failed request should
// be attempted again. Non-transient provider errors (bad request, auth
// failures, provider configuration problems) are never retried.
func isRetryableProviderRequestError(err error) bool {
	if err == nil {
		return false
	}
	code, _, _, isProviderStatus := providerErrorInfo(err)
	if isProviderStatus {
		return isRetryableProviderStatus(code)
	}
	msgLower := strings.ToLower(err.Error())
	if !strings.Contains(msgLower, "request failed: ") {
		return false
	}
	for _, marker := range transientNetworkErrorMarkers {
		if strings.Contains(msgLower, marker) {
			return true
		}
	}
	return false
}

// providerRetryBackoffDelay computes the exponential back-off delay before
// retry attempt+1. attempt is 1-based (the delay after the first failure).
func providerRetryBackoffDelay(attempt int, base, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if base <= 0 {
		base = 2 * time.Second
	}
	if attempt > 20 {
		attempt = 1
	}
	delay := base * time.Duration(1<<(attempt-1))
	if max > 0 && delay > max {
		delay = max
	}
	return delay
}

// effectiveProviderRetryAttempts clamps the chat-configured attempt count.
func effectiveProviderRetryAttempts(opts ProviderRetryOptions) int {
	if !opts.Enabled || opts.MaxAttempts < 2 {
		return 1
	}
	attempts := opts.MaxAttempts
	if attempts > maxProviderRetryAttempts {
		attempts = maxProviderRetryAttempts
	}
	return attempts
}

// extractProviderErrorDetail parses a provider error response body and
// returns the most descriptive (provider, message) pair available. OpenAI
// compatible APIs put the message in error.message; OpenRouter nests the
// upstream provider name and raw error under metadata.
func extractProviderErrorDetail(code int, body string) (string, string) {
	summary := fmt.Sprintf("HTTP %d", code)
	if strings.TrimSpace(body) == "" {
		return summary, ""
	}
	trimmed := strings.TrimSpace(body)
	var payload struct {
		Error    json.RawMessage `json:"error"`
		Message  string          `json:"message"`
		Metadata struct {
			ProviderName string `json:"provider_name"`
			Raw          string `json:"raw"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		// Not JSON: show a truncated version of the raw body.
		if len(trimmed) > 300 {
			trimmed = trimmed[:300] + "…"
		}
		return summary, trimmed
	}

	providerMessage := payload.Message
	providerName := payload.Metadata.ProviderName
	if len(payload.Error) > 0 {
		var errObj struct {
			Message  string      `json:"message"`
			Code     interface{} `json:"code"`
			Metadata struct {
				ProviderName string `json:"provider_name"`
				Raw          string `json:"raw"`
			} `json:"metadata"`
		}
		if json.Unmarshal(payload.Error, &errObj) == nil && strings.TrimSpace(errObj.Message) != "" {
			providerMessage = strings.TrimSpace(errObj.Message)
			if errObj.Metadata.ProviderName != "" {
				providerName = errObj.Metadata.ProviderName
			}
		} else {
			// error was a plain string (older APIs)
			var errStr string
			if json.Unmarshal(payload.Error, &errStr) == nil && strings.TrimSpace(errStr) != "" {
				providerMessage = strings.TrimSpace(errStr)
			}
		}
	}
	if len(providerMessage) > 300 {
		providerMessage = providerMessage[:300] + "…"
	}
	if providerMessage != "" {
		summary = fmt.Sprintf("%s (%s)", summary, providerMessage)
	}
	return summary, providerName
}

// describeProviderFailure turns a provider failure into a short human
// readable summary (used by widget messages) and reports the attempts used.
func describeProviderFailure(err error) (string, int) {
	if err == nil {
		return "", 0
	}
	code, body, attempts, ok := providerErrorInfo(err)
	if !ok || attempts < 1 {
		attempts = 1
	}
	if code == 0 {
		return "", attempts
	}
	summary, _ := extractProviderErrorDetail(code, body)
	return summary, attempts
}

// Failure reason values recorded in message metadata ("failure_reason") and
// reused to pick user-facing guidance. They distinguish a genuine provider-side
// failure (or a recognised transient network problem) from an unclassified
// internal error, so the chat never speculates about credits or outages.
const (
	FailureReasonContextWindow    = "context_window_exceeded"
	FailureReasonProviderError    = "provider_error"
	FailureReasonTransientNetwork = "transient_network_error"
	FailureReasonTimeout          = "turn_timeout"
	FailureReasonInternal         = "internal_error"
	// FailureReasonLeakedCompaction marks a turn whose assistant output was only
	// a leaked context-compaction transcript (ci#562): the model volunteered its
	// own bookkeeping instead of an answer, twice in a row, so the turn is
	// stopped with guidance instead of being persisted with no text.
	FailureReasonLeakedCompaction = "leaked_compaction_transcript"
	// FailureReasonRepetitionLoop marks a round whose stream collapsed into an
	// unbounded repetition loop and was cut off early (ci#670). It is recorded
	// on the provider request for metrics; the turn itself finishes normally
	// with a short answer carrying the "stopped: repeated output detected" note.
	FailureReasonRepetitionLoop = "repetition_loop"
)

// GenerationFailureClassification is the outcome of classifying a failed
// generation. ProviderSummary and ProviderAttempts are only meaningful for
// FailureReasonProviderError.
type GenerationFailureClassification struct {
	Reason           string
	ProviderSummary  string
	ProviderAttempts int
}

// ClassifyGenerationFailure maps a generation error onto a stable failure
// reason. Only errors that are genuinely provider-side (a provider HTTP
// response) or a recognised transient network problem may later be described as
// a provider issue; everything else is reported as internal so the user is not
// told credits/outage are the cause.
func ClassifyGenerationFailure(err error) GenerationFailureClassification {
	if err == nil {
		return GenerationFailureClassification{Reason: FailureReasonInternal}
	}
	// A turn deadline (the bot reply task's asynq timeout) is not a provider
	// fault and not a user stop: report it as its own reason so the chat never
	// shows a bogus "I paused this response" for a turn that simply ran out of
	// time (ci#642). The context.Canceled case is handled by the caller as a
	// genuine stop, so it is deliberately not classified here.
	if errors.Is(err, context.DeadlineExceeded) {
		return GenerationFailureClassification{Reason: FailureReasonTimeout}
	}
	if isContextWindowExceededError(err) {
		return GenerationFailureClassification{Reason: FailureReasonContextWindow}
	}
	// A turn whose assistant output was only a leaked context-compaction
	// transcript has its own reason: it is neither a provider fault nor an
	// internal error, and the guidance tells the user the work done so far is
	// kept and how to continue (ci#562).
	if errors.Is(err, errLeakedCompactionTranscript) {
		return GenerationFailureClassification{Reason: FailureReasonLeakedCompaction}
	}
	summary, attempts := describeProviderFailure(err)
	if attempts < 1 {
		attempts = 1
	}
	if summary != "" {
		return GenerationFailureClassification{
			Reason:           FailureReasonProviderError,
			ProviderSummary:  summary,
			ProviderAttempts: attempts,
		}
	}
	if isTransientNetworkError(err) {
		return GenerationFailureClassification{
			Reason:           FailureReasonTransientNetwork,
			ProviderAttempts: attempts,
		}
	}
	return GenerationFailureClassification{Reason: FailureReasonInternal, ProviderAttempts: attempts}
}

// isTransientNetworkError reports whether err matches a connection-level
// failure that is recognised as transient (worth explaining as a connectivity
// problem rather than an internal fault).
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msgLower := strings.ToLower(err.Error())
	for _, marker := range transientNetworkErrorMarkers {
		if strings.Contains(msgLower, marker) {
			return true
		}
	}
	return false
}

// TruncateFailureDetail trims a raw error string for safe inclusion in a
// user-facing message.
func TruncateFailureDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) > 300 {
		// headOfString keeps the cut on a UTF-8 rune boundary: provider errors
		// echo request content (which is where a large tool dump ends up), and
		// a torn rune would put invalid UTF-8 into the user-facing message
		// instead of trimming it (ci#680).
		detail = headOfString(detail, 300) + "…"
	}
	return detail
}

// providerRetryOptionsFromConfig reads the optional provider retry flags from
// a chat config map. All flags are optional. provider_retry defaults to true so
// every interaction gets at least one retry after a transient pre-stream
// provider failure; a chat opts out explicitly with provider_retry: false.
//
//	provider_retry:            bool,   default true
//	provider_retry_max:        int     (total attempts, default 3)
//	provider_retry_backoff_ms: int     (base back-off delay, default 2000)
//	provider_retry_backoff_max_ms: int (back-off cap, default 30000)
func providerRetryOptionsFromConfig(configMap map[string]interface{}) ProviderRetryOptions {
	enabled := mapGetOrDefault[bool](configMap, "provider_retry", true)
	if !enabled {
		return ProviderRetryOptions{Enabled: false, MaxAttempts: 1}
	}
	maxAttempts := int(mapInt64OrDefault(configMap, "provider_retry_max", 3))
	baseBackoff := time.Duration(mapInt64OrDefault(configMap, "provider_retry_backoff_ms", 2000)) * time.Millisecond
	maxBackoff := time.Duration(mapInt64OrDefault(configMap, "provider_retry_backoff_max_ms", 30000)) * time.Millisecond
	return ProviderRetryOptions{
		Enabled:     true,
		MaxAttempts: maxAttempts,
		BaseBackoff: baseBackoff,
		MaxBackoff:  maxBackoff,
	}
}

// makeProviderRetryReporter builds the callback that posts a provider-retry
// widget message (data_type "event") to the chat after every failed provider
// attempt that will be retried. The retry_at timestamp lets clients render a
// live back-off countdown.
func (aih *AIHandlerImpl) makeProviderRetryReporter(chatUUID string) func(attempt int, maxAttempts int, retryErr error, retryAt time.Time) {
	return func(attempt int, maxAttempts int, retryErr error, retryAt time.Time) {
		detail := ""
		if summary, _ := describeProviderFailure(retryErr); summary != "" {
			detail = summary
		} else if retryErr != nil {
			detail = retryErr.Error()
		}
		meta := map[string]interface{}{
			"finished":   true,
			"event_type": "provider_retry",
			"provider_retry": map[string]interface{}{
				"attempt":      attempt,
				"max_attempts": maxAttempts,
				"status":       "retry_scheduled",
				"error_detail": detail,
				"retry_at":     retryAt.UTC().Format(time.RFC3339),
			},
		}
		dataType := "event"
		aih.botContext.Client.SendChatMessage(chatUUID, client.SendMessage{
			DataType: &dataType,
			MetaData: &meta,
		})
	}
}
