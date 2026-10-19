package msgmate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file owns the *configurable* infrastructure-retry policy (ci#703). The
// transient tool-retry and sandbox-capacity backoff of tool_failure_retry.go
// previously had fixed defaults with a single deployment-level env override
// each and could not be tuned per agent, so the ci#703 incident could only be
// mitigated by a hotfix that raised the caps for the whole deployment.
//
// The policy is now resolved as default -> deployment env -> the agent's
// resolved config map (the chat/agent config, which carries the bot runtime's
// shared-config keys), normalised to hard bounds so a misconfigured agent can
// never leave the retry loop unbounded or hammer the host. The resolved policy
// travels on the generation context, so every tool call of the turn uses the
// agent's own policy without threading another parameter through the streaming
// and tool-call machinery.

const (
	// ToolFailureRetryMaxDelayEnvKey is the deployment-level override of the
	// backoff cap (milliseconds) of a transient tool failure.
	ToolFailureRetryMaxDelayEnvKey = "OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_MAX_DELAY_MS"
	// SandboxCapacityRetryMaxDelayEnvKey is the deployment-level override of the
	// backoff cap (milliseconds) of a scheduler capacity failure.
	SandboxCapacityRetryMaxDelayEnvKey = "OCI_MSGMATE_CODER_SANDBOX_CAPACITY_RETRY_MAX_DELAY_MS"
	// maxToolFailureRetryAttempts is the hard cap of the configured attempt
	// count: a bad config can never turn a failing tool into an endless loop.
	maxToolFailureRetryAttempts = 8
	// maxToolFailureRetryDelayMillis bounds a configured backoff value (in
	// milliseconds) before it is converted to a time.Duration, so a hostile or
	// overflowing config/env value is rejected instead of silently wrapped
	// (ci#703, CodeQL go/incorrect-integer-conversion).
	maxToolFailureRetryDelayMillis = int64(24 * 60 * 60 * 1000) // 24h
)

// ErrToolFailureRetryValueOutOfRange reports a retry-policy value that does not
// fit the supported range (an attempt count outside int32, or a backoff outside
// ±maxToolFailureRetryDelayMillis). The caller keeps the previous/default value
// and logs the rejection instead of silently truncating the number.
var ErrToolFailureRetryValueOutOfRange = errors.New("tool failure retry value out of range")

// boundedToolFailureRetryCount narrows a configured attempt count to an int
// only after proving the value is representable, so the conversion cannot
// truncate (ci#703, CodeQL go/incorrect-integer-conversion). A value outside
// [math.MinInt32, math.MaxInt32] is refused with
// ErrToolFailureRetryValueOutOfRange; a small negative value is still narrowed
// and then clamped to 1 by normalizeToolFailureRetryOptions, which keeps the
// documented "never zero attempts" behaviour, and an oversized positive value
// is clamped to maxToolFailureRetryAttempts.
func boundedToolFailureRetryCount(raw int64) (int, error) {
	if raw < math.MinInt32 || raw > math.MaxInt32 {
		return 0, fmt.Errorf("%w: attempt count %d is outside [%d, %d]",
			ErrToolFailureRetryValueOutOfRange, raw, int64(math.MinInt32), int64(math.MaxInt32))
	}
	return int(raw), nil
}

// boundedToolFailureRetryDelay converts a configured backoff value in
// milliseconds into a time.Duration only after proving the value is bounded, so
// the conversion can neither truncate nor wrap (a value near math.MaxInt64
// would otherwise overflow the nanosecond multiplication). Out-of-range values
// are refused with ErrToolFailureRetryValueOutOfRange; a small negative value is
// kept and later clamped to 0 by normalizeToolFailureRetryOptions.
func boundedToolFailureRetryDelay(key string, millis int64) (time.Duration, error) {
	if millis < -maxToolFailureRetryDelayMillis || millis > maxToolFailureRetryDelayMillis {
		return 0, fmt.Errorf("%w: %s = %dms is outside [-%dms, %dms]",
			ErrToolFailureRetryValueOutOfRange, key, millis, maxToolFailureRetryDelayMillis, maxToolFailureRetryDelayMillis)
	}
	return time.Duration(millis) * time.Millisecond, nil
}

// ToolFailureRetryOptions is the effective infrastructure-retry policy of one
// turn: how often a transient tool failure is attempted, how the two backoff
// curves (a connection/resource blip and a scheduler-capacity rejection) are
// shaped, and whether the retrying is on at all.
type ToolFailureRetryOptions struct {
	// Enabled is false when the agent explicitly opts out with
	// tool_failure_retry: false; a disabled policy attempts exactly once.
	Enabled bool
	// MaxAttempts is the total attempt count (the first attempt included),
	// clamped to [1, maxToolFailureRetryAttempts].
	MaxAttempts int
	// BaseDelay and MaxDelay bound the exponential blip backoff.
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// CapacityBaseDelay and CapacityMaxDelay bound the linear
	// scheduler-capacity backoff.
	CapacityBaseDelay time.Duration
	CapacityMaxDelay  time.Duration
}

// toolFailureRetryOptionsFromConfig resolves the effective infrastructure-retry
// policy as default -> deployment env -> this agent's config.
//
// Config keys (all optional; the deployment env, then the code defaults, fill
// the gaps):
//
//	tool_failure_retry:                   bool, default true
//	tool_failure_retry_max:               int   (total attempts, default 5)
//	tool_failure_retry_base_delay_ms:     int   (blip backoff base, default 500)
//	tool_failure_retry_max_delay_ms:      int   (blip backoff cap, default 5000)
//	sandbox_capacity_retry_base_delay_ms: int   (capacity backoff base, default 5000)
//	sandbox_capacity_retry_max_delay_ms:  int   (capacity backoff cap, default 30000)
func toolFailureRetryOptionsFromConfig(configMap map[string]interface{}) ToolFailureRetryOptions {
	opts := ToolFailureRetryOptions{
		Enabled:           true,
		MaxAttempts:       DefaultToolFailureRetryAttempts,
		BaseDelay:         DefaultToolFailureRetryBaseDelay,
		MaxDelay:          DefaultToolFailureRetryMaxDelay,
		CapacityBaseDelay: DefaultSandboxCapacityRetryBaseDelay,
		CapacityMaxDelay:  DefaultSandboxCapacityRetryMaxDelay,
	}
	if v, ok := envInt64(ToolFailureRetryAttemptsEnvKey); ok {
		if attempts, err := boundedToolFailureRetryCount(v); err != nil {
			logToolFailureRetryValueRejected(ToolFailureRetryAttemptsEnvKey, err)
		} else {
			opts.MaxAttempts = attempts
		}
	}
	if v, ok := envInt64(ToolFailureRetryBaseDelayEnvKey); ok {
		if delay, err := boundedToolFailureRetryDelay(ToolFailureRetryBaseDelayEnvKey, v); err != nil {
			logToolFailureRetryValueRejected(ToolFailureRetryBaseDelayEnvKey, err)
		} else {
			opts.BaseDelay = delay
		}
	}
	if v, ok := envInt64(ToolFailureRetryMaxDelayEnvKey); ok {
		if delay, err := boundedToolFailureRetryDelay(ToolFailureRetryMaxDelayEnvKey, v); err != nil {
			logToolFailureRetryValueRejected(ToolFailureRetryMaxDelayEnvKey, err)
		} else {
			opts.MaxDelay = delay
		}
	}
	if v, ok := envInt64(SandboxCapacityRetryBaseDelayEnvKey); ok {
		if delay, err := boundedToolFailureRetryDelay(SandboxCapacityRetryBaseDelayEnvKey, v); err != nil {
			logToolFailureRetryValueRejected(SandboxCapacityRetryBaseDelayEnvKey, err)
		} else {
			opts.CapacityBaseDelay = delay
		}
	}
	if v, ok := envInt64(SandboxCapacityRetryMaxDelayEnvKey); ok {
		if delay, err := boundedToolFailureRetryDelay(SandboxCapacityRetryMaxDelayEnvKey, v); err != nil {
			logToolFailureRetryValueRejected(SandboxCapacityRetryMaxDelayEnvKey, err)
		} else {
			opts.CapacityMaxDelay = delay
		}
	}
	if v, ok := configMap["tool_failure_retry"].(bool); ok {
		opts.Enabled = v
	}
	// The agent config wins over the env, but every value is range-checked
	// before it is narrowed: an out-of-range value is refused (and logged)
	// instead of truncating into a nonsensical policy.
	if attempts, err := boundedToolFailureRetryCount(mapInt64OrDefault(configMap, "tool_failure_retry_max", int64(opts.MaxAttempts))); err != nil {
		logToolFailureRetryValueRejected("tool_failure_retry_max", err)
	} else {
		opts.MaxAttempts = attempts
	}
	if delay, err := boundedToolFailureRetryDelay("tool_failure_retry_base_delay_ms",
		mapInt64OrDefault(configMap, "tool_failure_retry_base_delay_ms", int64(opts.BaseDelay/time.Millisecond))); err != nil {
		logToolFailureRetryValueRejected("tool_failure_retry_base_delay_ms", err)
	} else {
		opts.BaseDelay = delay
	}
	if delay, err := boundedToolFailureRetryDelay("tool_failure_retry_max_delay_ms",
		mapInt64OrDefault(configMap, "tool_failure_retry_max_delay_ms", int64(opts.MaxDelay/time.Millisecond))); err != nil {
		logToolFailureRetryValueRejected("tool_failure_retry_max_delay_ms", err)
	} else {
		opts.MaxDelay = delay
	}
	if delay, err := boundedToolFailureRetryDelay("sandbox_capacity_retry_base_delay_ms",
		mapInt64OrDefault(configMap, "sandbox_capacity_retry_base_delay_ms", int64(opts.CapacityBaseDelay/time.Millisecond))); err != nil {
		logToolFailureRetryValueRejected("sandbox_capacity_retry_base_delay_ms", err)
	} else {
		opts.CapacityBaseDelay = delay
	}
	if delay, err := boundedToolFailureRetryDelay("sandbox_capacity_retry_max_delay_ms",
		mapInt64OrDefault(configMap, "sandbox_capacity_retry_max_delay_ms", int64(opts.CapacityMaxDelay/time.Millisecond))); err != nil {
		logToolFailureRetryValueRejected("sandbox_capacity_retry_max_delay_ms", err)
	} else {
		opts.CapacityMaxDelay = delay
	}

	return normalizeToolFailureRetryOptions(opts)
}

// logToolFailureRetryValueRejected reports a rejected retry-policy value. The
// resolved policy keeps the default/env value instead, so a misconfigured
// agent still gets bounded retrying rather than a wrapped or zeroed value.
func logToolFailureRetryValueRejected(key string, err error) {
	log.Printf("[tool_failure_retry] ignoring invalid retry policy value %q: %v (ci#703)", key, err)
}

// normalizeToolFailureRetryOptions clamps a resolved policy into the supported
// range so a single bad config value cannot defeat the retry bounds.
func normalizeToolFailureRetryOptions(opts ToolFailureRetryOptions) ToolFailureRetryOptions {
	if !opts.Enabled {
		opts.MaxAttempts = 1
		return opts
	}
	if opts.MaxAttempts < 1 {
		opts.MaxAttempts = 1
	}
	if opts.MaxAttempts > maxToolFailureRetryAttempts {
		opts.MaxAttempts = maxToolFailureRetryAttempts
	}
	if opts.BaseDelay < 0 {
		opts.BaseDelay = 0
	}
	if opts.MaxDelay < 0 {
		opts.MaxDelay = 0
	}
	if opts.MaxDelay > 0 && opts.MaxDelay < opts.BaseDelay {
		opts.MaxDelay = opts.BaseDelay
	}
	if opts.CapacityBaseDelay < 0 {
		opts.CapacityBaseDelay = 0
	}
	if opts.CapacityMaxDelay < 0 {
		opts.CapacityMaxDelay = 0
	}
	if opts.CapacityMaxDelay > 0 && opts.CapacityMaxDelay < opts.CapacityBaseDelay {
		opts.CapacityMaxDelay = opts.CapacityBaseDelay
	}
	return opts
}

type toolFailureRetryOptionsContextKey struct{}

// withToolFailureRetryOptions attaches a resolved retry policy to the
// generation context. The tool loop reads it back with
// resolveToolFailureRetryOptions, so a turn retries with the policy that was
// resolved for the agent that started it (ci#703).
func withToolFailureRetryOptions(ctx context.Context, opts ToolFailureRetryOptions) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, toolFailureRetryOptionsContextKey{}, normalizeToolFailureRetryOptions(opts))
}

// resolveToolFailureRetryOptions returns the policy attached to the context,
// falling back to the deployment env / code defaults when the context carries
// none (eg a direct unit-test call).
func resolveToolFailureRetryOptions(ctx context.Context) ToolFailureRetryOptions {
	if ctx != nil {
		if opts, ok := ctx.Value(toolFailureRetryOptionsContextKey{}).(ToolFailureRetryOptions); ok {
			return opts
		}
	}
	return toolFailureRetryOptionsFromConfig(nil)
}

// toolFailureBackoffDelayWithOptions is toolFailureBackoffDelay against a
// resolved policy: the exponential blip backoff capped by opts.MaxDelay.
func toolFailureBackoffDelayWithOptions(attempt int, opts ToolFailureRetryOptions) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 16 {
		attempt = 16
	}
	if opts.BaseDelay <= 0 {
		return 0
	}
	delay := opts.BaseDelay * time.Duration(1<<(attempt-1))
	if opts.MaxDelay > 0 && delay > opts.MaxDelay {
		delay = opts.MaxDelay
	}
	return delay
}

// toolFailureRetryWaitWithOptions is the actual blip wait before retry
// attempt+1: the configured exponential backoff plus a jitter bounded to a
// quarter of the delay, so the agents of a saturated sandbox pool do not
// re-provision in lockstep and re-create the burst (ci#688).
func toolFailureRetryWaitWithOptions(attempt int, opts ToolFailureRetryOptions) time.Duration {
	delay := toolFailureBackoffDelayWithOptions(attempt, opts)
	if delay <= 0 {
		return 0
	}
	jitter := time.Duration(rand.Int64N(int64(delay/4) + 1))
	return delay + jitter
}

// sandboxCapacityBackoffDelayWithOptions is sandboxCapacityBackoffDelay against
// a resolved policy: a linear ramp from the configured base, capped.
func sandboxCapacityBackoffDelayWithOptions(attempt int, opts ToolFailureRetryOptions) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 16 {
		attempt = 16
	}
	if opts.CapacityBaseDelay <= 0 {
		return 0
	}
	delay := opts.CapacityBaseDelay * time.Duration(attempt)
	if opts.CapacityMaxDelay > 0 && delay > opts.CapacityMaxDelay {
		delay = opts.CapacityMaxDelay
	}
	return delay
}

// sandboxCapacityRetryWaitWithOptions is the actual capacity wait before retry
// attempt+1: the configured linear ramp plus a jitter bounded to half the
// delay, so the agents that lost the scheduling race do not retry in lockstep
// (ci#691).
func sandboxCapacityRetryWaitWithOptions(attempt int, opts ToolFailureRetryOptions) time.Duration {
	delay := sandboxCapacityBackoffDelayWithOptions(attempt, opts)
	if delay <= 0 {
		return 0
	}
	jitter := time.Duration(rand.Int64N(int64(delay/2) + 1))
	return delay + jitter
}

// envInt64 reads a non-negative integer deployment-env override.
func envInt64(key string) (int64, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}
