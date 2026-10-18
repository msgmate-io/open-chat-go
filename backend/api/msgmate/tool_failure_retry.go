package msgmate

import (
	"context"
	"log"
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
	// failure is attempted in total (the first attempt included). Five
	// attempts absorb the bursty provisioning failures reported in ci#688
	// (and the ci#703 incident, where three attempts aborted the turn while
	// the sandbox pool was still saturated) without delaying a genuinely
	// broken tool for long. Per-agent override:
	// tool_failure_retry_max / DefaultToolFailureRetryAttempts.
	DefaultToolFailureRetryAttempts = 5
	// DefaultToolFailureRetryBaseDelay is the backoff before the second
	// attempt; it doubles per attempt and is capped by
	// DefaultToolFailureRetryMaxDelay.
	DefaultToolFailureRetryBaseDelay = 500 * time.Millisecond
	// DefaultToolFailureRetryMaxDelay caps a single backoff wait.
	DefaultToolFailureRetryMaxDelay = 5 * time.Second

	// ToolFailureRetryAttemptsEnvKey and ToolFailureRetryBaseDelayEnvKey are
	// the deployment-level overrides of the two knobs above, using the same
	// OCI_MSGMATE_CODER_ prefix as the other native-coder settings.
	ToolFailureRetryAttemptsEnvKey  = "OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_ATTEMPTS"
	ToolFailureRetryBaseDelayEnvKey = "OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_BASE_DELAY_MS"

	// DefaultSandboxCapacityRetryBaseDelay and
	// DefaultSandboxCapacityRetryMaxDelay bound the wait before re-attempting
	// a call whose sandbox could not be *scheduled* (ci#691). A scheduler
	// capacity problem ("0/1 nodes are available: 1 Insufficient cpu") clears
	// on the order of tens of seconds — when a finished sandbox is reaped or a
	// peer agent releases its pod — so the millisecond-scale tool backoff of
	// ci#688 cannot absorb it; a slightly more patient, still bounded wait can.
	DefaultSandboxCapacityRetryBaseDelay = 5 * time.Second
	DefaultSandboxCapacityRetryMaxDelay  = 30 * time.Second

	// SandboxCapacityRetryBaseDelayEnvKey is the deployment-level override of
	// the capacity backoff base (milliseconds).
	SandboxCapacityRetryBaseDelayEnvKey = "OCI_MSGMATE_CODER_SANDBOX_CAPACITY_RETRY_BASE_DELAY_MS"
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

// sandboxCapacityFailureMarkers are the lower-cased fragments of a *scheduler
// capacity* rejection: the Kubernetes scheduler could not place the sandbox pod
// because the node has no room left, e.g.
//
//	sandbox "coder-7b205aa3-…" is not schedulable: Unschedulable:
//	0/1 nodes are available: 1 Insufficient cpu. no new claims to deallocate,
//	preemption: 0/1 nodes are available: 1 No preemption victims found for
//	incoming pod.
//
// They are a subset of transientToolFailureMarkers: such a failure is
// environmental and retryable, but unlike a connection blip it is *not* cleared
// by a sub-second wait — the node only frees capacity when a finished sandbox
// is reaped or a peer agent releases its pod, which takes tens of seconds. The
// retry policy therefore gives it a more patient, still bounded backoff
// (ci#691).
var sandboxCapacityFailureMarkers = []string{
	"insufficient cpu",
	"insufficient memory",
	"insufficient ephemeral-storage",
	"insufficient pods",
	"is not schedulable",
	"unschedulable",
	"no preemption victims",
	"no new claims to deallocate",
	"nodes are available",
}

// isSandboxCapacityFailure reports whether a tool error is a scheduler capacity
// rejection (see sandboxCapacityFailureMarkers) rather than a connection or
// host-resource blip. A nil error is never a capacity failure.
func isSandboxCapacityFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range sandboxCapacityFailureMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
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

// toolFailureRetryAttempts resolves the configured attempt count (default ->
// deployment env override -> clamp), ignoring any agent config.
//
// Deprecated: the effective policy of a turn is
// toolFailureRetryOptionsFromConfig / resolveToolFailureRetryOptions; this
// helper remains for callers without a config map (ci#703).
func toolFailureRetryAttempts() int {
	return toolFailureRetryOptionsFromConfig(nil).MaxAttempts
}

// toolFailureRetryBaseDelay resolves the configured backoff base (deployment
// env override in milliseconds), ignoring any agent config.
//
// Deprecated: see toolFailureRetryAttempts.
func toolFailureRetryBaseDelay() time.Duration {
	return toolFailureRetryOptionsFromConfig(nil).BaseDelay
}

// toolFailureBackoffDelay is the wait before attempt+1 (attempt is 1-based, so
// the first retry waits base, then 2*base, ...) capped by the configured
// maximum.
//
// Deprecated: policy-aware callers use toolFailureBackoffDelayWithOptions.
func toolFailureBackoffDelay(attempt int) time.Duration {
	return toolFailureBackoffDelayWithOptions(attempt, toolFailureRetryOptionsFromConfig(nil))
}

// sandboxCapacityRetryBaseDelay resolves the configured capacity backoff base
// (deployment env override in milliseconds; ci#691), ignoring any agent config.
//
// Deprecated: see toolFailureRetryAttempts.
func sandboxCapacityRetryBaseDelay() time.Duration {
	return toolFailureRetryOptionsFromConfig(nil).CapacityBaseDelay
}

// sandboxCapacityBackoffDelay is the wait before retry attempt+1 for a
// scheduler *capacity* rejection (ci#691): a linear ramp from the configured
// base (5s by default) capped by the configured maximum (30s by default).
//
// It is deliberately much slower than toolFailureBackoffDelay: the node only
// frees a sandbox's request when a finished sandbox is reaped or a peer agent
// releases its pod, so hammering the scheduler every few hundred milliseconds
// would just burn the attempt budget on a condition that cannot have changed.
//
// Deprecated: policy-aware callers use sandboxCapacityBackoffDelayWithOptions.
func sandboxCapacityBackoffDelay(attempt int) time.Duration {
	return sandboxCapacityBackoffDelayWithOptions(attempt, toolFailureRetryOptionsFromConfig(nil))
}

// runToolWithTransientRetry executes a tool body, retrying a transient
// environmental failure with an exponential backoff. It returns the last
// result, the last error and how many extra attempts were made. The generation
// context bounds everything: once it is cancelled (the user pressed stop) the
// retrying stops immediately instead of re-running the tool body.
//
// A scheduler *capacity* rejection (an unschedulable sandbox pod, ci#691) is
// retried with the slower, still bounded capacity backoff instead of the
// millisecond-scale blip backoff: the condition only clears when the node frees
// capacity, which takes tens of seconds.
func runToolWithTransientRetry(ctx context.Context, toolName string, run func() (string, error)) (string, error, int) {
	// ci#703: the retry policy of this turn (attempts + both backoff curves) is
	// resolved from the deployment env and the agent's own config and carried on
	// the generation context; it falls back to the env/defaults when no policy
	// was attached.
	opts := resolveToolFailureRetryOptions(ctx)
	attempts := opts.MaxAttempts
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
		if isSandboxCapacityFailure(runErr) {
			delay := sandboxCapacityRetryWaitWithOptions(attempt, opts)
			log.Printf(
				"Tool %s could not be scheduled (attempt %d/%d): %v; retrying in %s (ci#691)",
				toolName, attempt, attempts, runErr, delay,
			)
			select {
			case <-ctx.Done():
				return result, runErr, retries
			case <-time.After(delay):
			}
			retries++
			continue
		}
		delay := toolFailureRetryWaitWithOptions(attempt, opts)
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

// sandboxCapacityRetryWait is the actual capacity wait before retry attempt+1:
// the linear ramp of sandboxCapacityBackoffDelay plus a bounded jitter, so the
// agents that lost the scheduling race do not all retry in lockstep and
// re-create the burst that saturated the node (ci#691). The jitter is capped at
// half the delay, so the wait stays within one capacity window.
// Deprecated: policy-aware callers use sandboxCapacityRetryWaitWithOptions.
func sandboxCapacityRetryWait(attempt int) time.Duration {
	return sandboxCapacityRetryWaitWithOptions(attempt, toolFailureRetryOptionsFromConfig(nil))
}

// toolFailureRetryWait is the actual wait before retry attempt+1: the
// deterministic backoff of toolFailureBackoffDelay plus a small jitter, so the
// agents of a saturated sandbox pool do not re-provision in lockstep and
// re-create the very burst that caused the failure (ci#688). The jitter is
// bounded to a quarter of the delay, so the wait stays predictable.
// Deprecated: policy-aware callers use toolFailureRetryWaitWithOptions.
func toolFailureRetryWait(attempt int) time.Duration {
	return toolFailureRetryWaitWithOptions(attempt, toolFailureRetryOptionsFromConfig(nil))
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
