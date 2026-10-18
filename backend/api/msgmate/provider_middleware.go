package msgmate

import (
	"strings"
	"sync"
	"time"

	"backend/nativecoder"
)

// ProviderRequestContext carries the state of one provider completion request
// through the OnRequest phase. Middleware implementations may mutate
// RequestBody (for example to enable usage reporting) before the request is
// serialized and sent.
type ProviderRequestContext struct {
	ChatUUID    string
	Endpoint    string
	Backend     string
	Model       string
	TrackUsage  bool
	RequestBody map[string]interface{}
}

// ProviderRequestRecord is handed to middleware in the OnResponse phase after
// the provider response finished streaming or the request failed. The record
// describes exactly one HTTP request attempt (including failed retries).
type ProviderRequestRecord struct {
	ProviderRequestContext
	// Attempt is the 1-based attempt number of this HTTP request within its
	// tool-call round.
	Attempt int
	// StatusCode is the HTTP response status (0 when the request never got a
	// response, e.g. connection failure).
	StatusCode int
	// Err is non-nil when the request failed (pre-stream provider failure or
	// mid-stream error).
	Err error
	// Duration covers time from request start until the stream ended (or the
	// error was raised).
	Duration time.Duration
	// Usage is the token accounting reported by the provider, if any.
	Usage *TokenUsage
	// Response holds the aggregated response parameters: the assembled
	// assistant content, the tool call invoked (if any), and raw response
	// fields worth keeping. Raw SSE chunks are intentionally not retained.
	Response map[string]interface{}
}

// ProviderRequestMiddleware observes (and optionally modifies) provider LLM
// completion requests. Implementations are registered by integrations (e.g.
// the admin DB-management integration for usage tracking and request logging)
// and only run for chats whose shared config enables the respective feature
// (e.g. "track_usage").
type ProviderRequestMiddleware interface {
	OnRequest(ctx *ProviderRequestContext)
	OnResponse(record *ProviderRequestRecord)
}

var (
	providerRequestMiddlewareMu    sync.RWMutex
	providerRequestMiddlewareOrder []string
	providerRequestMiddlewareReg   = map[string]ProviderRequestMiddleware{}
)

// RegisterProviderRequestMiddleware registers a middleware under a unique
// name. Registration order determines invocation order; re-registering a name
// replaces it in place.
func RegisterProviderRequestMiddleware(name string, middleware ProviderRequestMiddleware) {
	providerRequestMiddlewareMu.Lock()
	defer providerRequestMiddlewareMu.Unlock()
	name = strings.ToLower(strings.TrimSpace(name))
	if _, exists := providerRequestMiddlewareReg[name]; !exists {
		providerRequestMiddlewareOrder = append(providerRequestMiddlewareOrder, name)
	}
	providerRequestMiddlewareReg[name] = middleware
}

// providerRequestMeta threads per-completion metadata (chat identity and
// feature flags) through the streaming pipeline.
type providerRequestMeta struct {
	ChatUUID   string
	TrackUsage bool
	// Attempt is the 1-based attempt number of the current HTTP request
	// within its tool-call round.
	Attempt int
	// PostConfirmationContinuation marks a turn that continues an interaction
	// after a confirmable action executed. Such turns are retried once when the
	// model returns an assistant message with neither text nor tool calls, so an
	// intermittent provider emission cannot silently abandon the task (ci#428).
	PostConfirmationContinuation bool
	// ProviderAdmission is the effective per-key in-flight policy of the chat
	// (ci#620). A zero MaxConcurrency disables admission, which keeps tests and
	// non-coder deployments on the previous behaviour.
	ProviderAdmission ProviderAdmissionConfig
	// ProviderAdmissionKey is the secret-free identity of the provider key the
	// request is made with, so two keys against one provider are admitted
	// independently.
	ProviderAdmissionKey string
	// Budget is the per-chat tool-call budget the caller already resolved for
	// this turn (ci#709). When nil the tool loop falls back to the caps it was
	// passed, so a direct caller (a unit test) keeps its explicit values.
	Budget *nativecoder.ToolCallBudget
	// Outcome receives how the turn ended and its consumed counters, so the
	// reader can report them as structured state instead of only an error
	// string (ci#709). Optional.
	Outcome *TurnOutcome
}

// runProviderRequestMiddleware invokes all registered middlewares in the
// OnRequest phase. It is a no-op unless usage tracking (or a future
// track_usage-gated feature) is enabled for the chat.
func runProviderRequestMiddleware(ctx *ProviderRequestContext) {
	if !ctx.TrackUsage {
		return
	}
	for _, middleware := range providerRequestMiddlewares() {
		middleware.OnRequest(ctx)
	}
}

// reportProviderRequestResult invokes all registered middlewares in the
// OnResponse phase. Only enabled chats (TrackUsage) produce records.
func reportProviderRequestResult(ctx *ProviderRequestContext, attempt int, statusCode int, requestErr error, duration time.Duration, usage *TokenUsage, response map[string]interface{}) {
	if !ctx.TrackUsage {
		return
	}
	for _, middleware := range providerRequestMiddlewares() {
		middleware.OnResponse(&ProviderRequestRecord{
			ProviderRequestContext: *ctx,
			Attempt:                attempt,
			StatusCode:             statusCode,
			Err:                    requestErr,
			Duration:               duration,
			Usage:                  usage,
			Response:               response,
		})
	}
}

// providerRequestMiddlewares returns a snapshot of the registered middlewares
// in registration order.
func providerRequestMiddlewares() []ProviderRequestMiddleware {
	providerRequestMiddlewareMu.RLock()
	defer providerRequestMiddlewareMu.RUnlock()
	if len(providerRequestMiddlewareOrder) == 0 {
		return nil
	}
	out := make([]ProviderRequestMiddleware, 0, len(providerRequestMiddlewareOrder))
	for _, name := range providerRequestMiddlewareOrder {
		out = append(out, providerRequestMiddlewareReg[name])
	}
	return out
}
