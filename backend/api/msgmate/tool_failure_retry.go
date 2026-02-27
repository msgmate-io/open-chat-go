package msgmate

import (
	"context"
	"log"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file owns the transient *tool* failure policy of the core tool loop
// (ci#688). It is deliberately separate from provider_retry.go, which retries
// the provider request: a tool body can fail for purely environmental reasons —
// a per-chat coding sandbox could not be created under high parallelism
// ("failed to create temporary sandbox: Cloning ... error: copy-fd: ..."), the
// host ran out of file descriptors / disk, or the sandbox integration host was
// momentarily unreachable.
//
// Before this, each such blip was a hard tool failure: the agent burned its
// failed-tool budget (3 by default), aborted the interaction and (in plan mode)
// re-cloned and retried by itself, which made the exhaustion worse. The policy
// here is:
//
//  1. a recognised transient tool failure is retried with an exponential
//     backoff, honouring the generation context, so a blip is transparent;
//  2. a transient failure that survives the retries is reported to the model as
//     a failed call but does **not** consume the failed-tool budget — the total
//     tool-call cap still bounds the turn, so an agent cannot loop forever;
//  3. retries never resurrect an aborted turn: a cancelled context (the stop
//     button) ends the retrying immediately.

const (
	// DefaultToolFailureRetryAttempts is how many times a transient tool
	// failure is attempted in total (the first attempt included). Three
	// attempts absorb the bursty provisioning failures reported in ci#688
	// without delaying a genuinely broken tool.
	DefaultToolFailureRetryAttempts = 3
	// DefaultToolFailureRetryBaseDelay is the backoff before the second
	// attempt; it doubles per attempt and is capped by
	// DefaultToolFailureRetryMaxDelay.
	DefaultToolFailureRetryBaseDelay = 400 * time.Millisecond
	// DefaultToolFailureRetryMaxDelay caps a single backoff wait.
	DefaultToolFailureRetryMaxDelay = 5 * time.Second

	// ToolFailureRetryAttemptsEnvKey and ToolFailureRetryBaseDelayEnvKey are
	// the deployment-level overrides of the two knobs above, using the same
	// OCI_MSGMATE_CODER_ prefix as the other native-coder settings.
	ToolFailureRetryAttemptsEnvKey  = "OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_ATTEMPTS"
	ToolFailureRetryBaseDelayEnvKey = "OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_BASE_DELAY_MS"
)

// transientToolFailureMarkers are the lower-cased fragments that mark a tool
// failure as environmental rather than a model/tool misuse. The list is
// intentionally about *infrastructure*: sandbox provisioning, host resource
// exhaustion and connection-level blips between the core and the integration
// host. Anything the model can fix by itself (a bad argument, a missing file, a
// permission error) is deliberately absent, so it is never retried and always
// counts against the failed-tool budget.
var transientToolFailureMarkers = []string{
	// Per-chat coding-sandbox provisioning under load (ci#688).
	"failed to create temporary sandbox",
	"failed to provision temporary sandbox",
	"create temporary sandbox",
	"failed to bind",
	"copy-fd",
	"cloning into",
	"sandbox is not schedulable",
	"unschedulable",
	"insufficient cpu",
	"insufficient memory",
	"is not reachable",
	"not reachable",
	// Host resource exhaustion (file descriptors, disk, memory).
	"too many open files",
	"resource temporarily unavailable",
	"cannot allocate memory",
	"no space left on device",
	"disk quota exceeded",
	// Connection-level blips towards the sandbox/integration host.
	"connection refused",
	"connection reset by peer",
	"broken pipe",
	"unexpected eof",
	"i/o timeout",
	"context deadline exceeded",
	"temporary failure in name resolution",
	"server misbehaving",
	"tls handshake timeout",
	"transport is closing",
	"502 bad gateway",
	"503 service unavailable",
	"504 gateway timeout",
	"429 too many requests",
	"failed to connect",
}

// isTransientToolFailure reports whether a tool error is an environmental,
// retryable failure (see transientToolFailureMarkers). A nil error is never
// transient.
func isTransientToolFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range transientToolFailureMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// toolFailureRetryAttempts resolves the configured attempt count (deployment
// env override, clamped to [1, 8]).
func toolFailureRetryAttempts() int {
	attempts := DefaultToolFailureRetryAttempts
	if raw := strings.TrimSpace(os.Getenv(ToolFailureRetryAttemptsEnvKey)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			attempts = parsed
		}
	}
	if attempts < 1 {
		return 1
	}
	if attempts > 8 {
		return 8
	}
	return attempts
}

// toolFailureRetryBaseDelay resolves the configured backoff base (deployment
// env override in milliseconds).
func toolFailureRetryBaseDelay() time.Duration {
	base := DefaultToolFailureRetryBaseDelay
	if raw := strings.TrimSpace(os.Getenv(ToolFailureRetryBaseDelayEnvKey)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			base = time.Duration(parsed) * time.Millisecond
		}
	}
	if base < 0 {
		return 0
	}
	return base
}

// toolFailureBackoffDelay is the wait before attempt+1 (attempt is 1-based, so
// the first retry waits base, then 2*base, ...) capped by
// DefaultToolFailureRetryMaxDelay.
func toolFailureBackoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 16 {
		attempt = 16
	}
	delay := toolFailureRetryBaseDelay() * time.Duration(1<<(attempt-1))
	if delay > DefaultToolFailureRetryMaxDelay {
		delay = DefaultToolFailureRetryMaxDelay
	}
	return delay
}

// runToolWithTransientRetry executes a tool body, retrying a transient
// environmental failure with an exponential backoff. It returns the last
// result, the last error and how many extra attempts were made. The generation
// context bounds everything: once it is cancelled (the user pressed stop) the
// retrying stops immediately instead of re-running the tool body.
func runToolWithTransientRetry(ctx context.Context, toolName string, run func() (string, error)) (string, error, int) {
	attempts := toolFailureRetryAttempts()
	var (
		result  string
		runErr  error
		retries int
	)
	for attempt := 1; ; attempt++ {
		result, runErr = run()
		if runErr == nil {
			return result, nil, retries
		}
		if ctx.Err() != nil || attempt >= attempts || !isTransientToolFailure(runErr) {
			return result, runErr, retries
		}
		delay := toolFailureRetryWait(attempt)
		log.Printf(
			"Tool %s failed transiently (attempt %d/%d): %v; retrying in %s (ci#688)",
			toolName, attempt, attempts, runErr, delay,
		)
		select {
		case <-ctx.Done():
			return result, runErr, retries
		case <-time.After(delay):
		}
		retries++
	}
}

// toolFailureRetryWait is the actual wait before retry attempt+1: the
// deterministic backoff of toolFailureBackoffDelay plus a small jitter, so the
// agents of a saturated sandbox pool do not re-provision in lockstep and
// re-create the very burst that caused the failure (ci#688). The jitter is
// bounded to a quarter of the delay, so the wait stays predictable.
func toolFailureRetryWait(attempt int) time.Duration {
	delay := toolFailureBackoffDelay(attempt)
	if delay <= 0 {
		return 0
	}
	jitter := time.Duration(rand.Int64N(int64(delay/4) + 1))
	return delay + jitter
}

// toolCallFailureAccounting splits one round's executed calls into the failures
// that consume the agent's failed-tool budget and the transient/environmental
// ones that do not (ci#688). Keeping it a pure function makes the budget rule
// unit-testable without a live provider.
func toolCallFailureAccounting(calls []executedToolCall) (failed, transient int) {
	for _, call := range calls {
		if call.status != ToolCallStatusFailed {
			continue
		}
		if call.budgetExempt {
			transient++
			continue
		}
		failed++
	}
	return failed, transient
}
