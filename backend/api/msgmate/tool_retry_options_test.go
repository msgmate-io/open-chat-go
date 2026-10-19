package msgmate

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"
)

// ci#703: the infrastructure-retry policy is now configurable per agent and
// resolved default -> deployment env -> agent config, clamped to hard bounds.

func TestToolFailureRetryOptionsDefaults(t *testing.T) {
	for _, key := range []string{
		ToolFailureRetryAttemptsEnvKey,
		ToolFailureRetryBaseDelayEnvKey,
		ToolFailureRetryMaxDelayEnvKey,
		SandboxCapacityRetryBaseDelayEnvKey,
		SandboxCapacityRetryMaxDelayEnvKey,
	} {
		t.Setenv(key, "")
	}
	opts := toolFailureRetryOptionsFromConfig(nil)
	if !opts.Enabled {
		t.Fatalf("retrying must be enabled by default, got %+v", opts)
	}
	if opts.MaxAttempts != DefaultToolFailureRetryAttempts {
		t.Fatalf("MaxAttempts = %d, want %d", opts.MaxAttempts, DefaultToolFailureRetryAttempts)
	}
	if opts.BaseDelay != DefaultToolFailureRetryBaseDelay {
		t.Fatalf("BaseDelay = %s, want %s", opts.BaseDelay, DefaultToolFailureRetryBaseDelay)
	}
	if opts.MaxDelay != DefaultToolFailureRetryMaxDelay {
		t.Fatalf("MaxDelay = %s, want %s", opts.MaxDelay, DefaultToolFailureRetryMaxDelay)
	}
	if opts.CapacityBaseDelay != DefaultSandboxCapacityRetryBaseDelay {
		t.Fatalf("CapacityBaseDelay = %s, want %s", opts.CapacityBaseDelay, DefaultSandboxCapacityRetryBaseDelay)
	}
	if opts.CapacityMaxDelay != DefaultSandboxCapacityRetryMaxDelay {
		t.Fatalf("CapacityMaxDelay = %s, want %s", opts.CapacityMaxDelay, DefaultSandboxCapacityRetryMaxDelay)
	}
}

func TestToolFailureRetryOptionsPerAgentOverride(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "")
	t.Setenv(ToolFailureRetryMaxDelayEnvKey, "")
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "")
	t.Setenv(SandboxCapacityRetryMaxDelayEnvKey, "")

	opts := toolFailureRetryOptionsFromConfig(map[string]interface{}{
		"tool_failure_retry":                   true,
		"tool_failure_retry_max":               7,
		"tool_failure_retry_base_delay_ms":     250,
		"tool_failure_retry_max_delay_ms":      2000,
		"sandbox_capacity_retry_base_delay_ms": 1000,
		"sandbox_capacity_retry_max_delay_ms":  4000,
	})
	if opts.MaxAttempts != 7 {
		t.Fatalf("MaxAttempts = %d, want 7", opts.MaxAttempts)
	}
	if opts.BaseDelay != 250*time.Millisecond || opts.MaxDelay != 2*time.Second {
		t.Fatalf("blip backoff = %s/%s, want 250ms/2s", opts.BaseDelay, opts.MaxDelay)
	}
	if opts.CapacityBaseDelay != time.Second || opts.CapacityMaxDelay != 4*time.Second {
		t.Fatalf("capacity backoff = %s/%s, want 1s/4s", opts.CapacityBaseDelay, opts.CapacityMaxDelay)
	}
	if got := toolFailureBackoffDelayWithOptions(5, opts); got != 2*time.Second {
		t.Fatalf("blip delay(5) = %s, want the 2s cap", got)
	}
	if got := sandboxCapacityBackoffDelayWithOptions(9, opts); got != 4*time.Second {
		t.Fatalf("capacity delay(9) = %s, want the 4s cap", got)
	}
}

func TestToolFailureRetryOptionsNumericShapesAndClamp(t *testing.T) {
	for _, key := range []string{
		ToolFailureRetryAttemptsEnvKey,
		ToolFailureRetryBaseDelayEnvKey,
		ToolFailureRetryMaxDelayEnvKey,
		SandboxCapacityRetryBaseDelayEnvKey,
		SandboxCapacityRetryMaxDelayEnvKey,
	} {
		t.Setenv(key, "")
	}

	// A JSON-decoded config (float64) is accepted, and a hostile attempt count
	// is clamped instead of turning the loop into an endless one.
	opts := toolFailureRetryOptionsFromConfig(map[string]interface{}{
		"tool_failure_retry_max":           999,
		"tool_failure_retry_base_delay_ms": float64(100),
	})
	if opts.MaxAttempts != maxToolFailureRetryAttempts {
		t.Fatalf("MaxAttempts = %d, want the clamp %d", opts.MaxAttempts, maxToolFailureRetryAttempts)
	}
	if opts.BaseDelay != 100*time.Millisecond {
		t.Fatalf("BaseDelay = %s, want 100ms", opts.BaseDelay)
	}

	// A zero/negative attempt count must still attempt once, never zero times.
	if got := toolFailureRetryOptionsFromConfig(map[string]interface{}{"tool_failure_retry_max": -3}); got.MaxAttempts != 1 {
		t.Fatalf("MaxAttempts = %d, want 1", got.MaxAttempts)
	}
	// A max below the base is raised to the base, so the ramp never shrinks.
	if got := toolFailureRetryOptionsFromConfig(map[string]interface{}{
		"tool_failure_retry_base_delay_ms": 5000,
		"tool_failure_retry_max_delay_ms":  10,
	}); got.MaxDelay != 5*time.Second {
		t.Fatalf("MaxDelay = %s, want 5s", got.MaxDelay)
	}
}

func TestToolFailureRetryOptionsDisabled(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "")
	opts := toolFailureRetryOptionsFromConfig(map[string]interface{}{"tool_failure_retry": false})
	if opts.Enabled || opts.MaxAttempts != 1 {
		t.Fatalf("disabled policy must attempt exactly once, got %+v", opts)
	}
}

// ci#703: the attempt count and the backoff values are range-checked before the
// narrowing conversion, so an out-of-range value is refused with a typed error
// instead of being truncated into a nonsense policy
// (CodeQL go/incorrect-integer-conversion).
func TestBoundedToolFailureRetryValues(t *testing.T) {
	for _, tc := range []struct {
		raw  int64
		want int
	}{
		{0, 0},
		{-3, -3}, // narrowed, then clamped to 1 by normalize
		{maxToolFailureRetryAttempts, maxToolFailureRetryAttempts},
		{999, 999}, // narrowed, then clamped to the hard cap by normalize
		{math.MaxInt32, math.MaxInt32},
		{math.MinInt32, math.MinInt32},
	} {
		got, err := boundedToolFailureRetryCount(tc.raw)
		if err != nil {
			t.Fatalf("boundedToolFailureRetryCount(%d) unexpected error: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("boundedToolFailureRetryCount(%d) = %d, want %d", tc.raw, got, tc.want)
		}
	}
	for _, raw := range []int64{math.MaxInt32 + 1, math.MaxInt64, math.MinInt32 - 1, math.MinInt64} {
		got, err := boundedToolFailureRetryCount(raw)
		if !errors.Is(err, ErrToolFailureRetryValueOutOfRange) {
			t.Fatalf("boundedToolFailureRetryCount(%d) err = %v, want ErrToolFailureRetryValueOutOfRange", raw, err)
		}
		if got != 0 {
			t.Fatalf("boundedToolFailureRetryCount(%d) = %d, want 0 on error", raw, got)
		}
	}

	for _, tc := range []struct {
		millis int64
		want   time.Duration
	}{
		{0, 0},
		{-50, -50 * time.Millisecond}, // kept, then clamped to 0 by normalize
		{250, 250 * time.Millisecond},
		{maxToolFailureRetryDelayMillis, time.Duration(maxToolFailureRetryDelayMillis) * time.Millisecond},
		{-maxToolFailureRetryDelayMillis, -time.Duration(maxToolFailureRetryDelayMillis) * time.Millisecond},
	} {
		got, err := boundedToolFailureRetryDelay("k", tc.millis)
		if err != nil {
			t.Fatalf("boundedToolFailureRetryDelay(%d) unexpected error: %v", tc.millis, err)
		}
		if got != tc.want {
			t.Fatalf("boundedToolFailureRetryDelay(%d) = %s, want %s", tc.millis, got, tc.want)
		}
	}
	for _, raw := range []int64{maxToolFailureRetryDelayMillis + 1, math.MaxInt64, -maxToolFailureRetryDelayMillis - 1, math.MinInt64} {
		got, err := boundedToolFailureRetryDelay("k", raw)
		if !errors.Is(err, ErrToolFailureRetryValueOutOfRange) {
			t.Fatalf("boundedToolFailureRetryDelay(%d) err = %v, want ErrToolFailureRetryValueOutOfRange", raw, err)
		}
		if got != 0 {
			t.Fatalf("boundedToolFailureRetryDelay(%d) = %s, want 0 on error", raw, got)
		}
	}
}

// An out-of-range config value must keep the resolved default instead of being
// truncated (an overflowing attempt count must never look like a small one).
func TestToolFailureRetryOptionsRejectsOverflowingValues(t *testing.T) {
	for _, key := range []string{
		ToolFailureRetryAttemptsEnvKey,
		ToolFailureRetryBaseDelayEnvKey,
		ToolFailureRetryMaxDelayEnvKey,
		SandboxCapacityRetryBaseDelayEnvKey,
		SandboxCapacityRetryMaxDelayEnvKey,
	} {
		t.Setenv(key, "")
	}

	opts := toolFailureRetryOptionsFromConfig(map[string]interface{}{
		"tool_failure_retry_max":               int64(math.MaxInt64),
		"tool_failure_retry_base_delay_ms":     int64(math.MaxInt64),
		"tool_failure_retry_max_delay_ms":      int64(math.MaxInt64),
		"sandbox_capacity_retry_base_delay_ms": int64(math.MaxInt64),
		"sandbox_capacity_retry_max_delay_ms":  int64(math.MaxInt64),
	})
	if opts.MaxAttempts != DefaultToolFailureRetryAttempts {
		t.Fatalf("MaxAttempts = %d, want the default %d (no truncation)", opts.MaxAttempts, DefaultToolFailureRetryAttempts)
	}
	if opts.BaseDelay != DefaultToolFailureRetryBaseDelay || opts.MaxDelay != DefaultToolFailureRetryMaxDelay {
		t.Fatalf("blip backoff = %s/%s, want the defaults %s/%s", opts.BaseDelay, opts.MaxDelay, DefaultToolFailureRetryBaseDelay, DefaultToolFailureRetryMaxDelay)
	}
	if opts.CapacityBaseDelay != DefaultSandboxCapacityRetryBaseDelay || opts.CapacityMaxDelay != DefaultSandboxCapacityRetryMaxDelay {
		t.Fatalf("capacity backoff = %s/%s, want the defaults %s/%s", opts.CapacityBaseDelay, opts.CapacityMaxDelay, DefaultSandboxCapacityRetryBaseDelay, DefaultSandboxCapacityRetryMaxDelay)
	}

	// A delay above the supported bound is refused too, while a valid sibling
	// key in the same map is still honoured.
	partial := toolFailureRetryOptionsFromConfig(map[string]interface{}{
		"tool_failure_retry_max_delay_ms":  int64(maxToolFailureRetryDelayMillis + 1),
		"tool_failure_retry_base_delay_ms": 250,
	})
	if partial.MaxDelay != DefaultToolFailureRetryMaxDelay {
		t.Fatalf("MaxDelay = %s, want the default %s", partial.MaxDelay, DefaultToolFailureRetryMaxDelay)
	}
	if partial.BaseDelay != 250*time.Millisecond {
		t.Fatalf("BaseDelay = %s, want 250ms", partial.BaseDelay)
	}

	// The deployment env is range-checked with the same bound.
	t.Setenv(ToolFailureRetryMaxDelayEnvKey, strconv.FormatInt(maxToolFailureRetryDelayMillis+1, 10))
	t.Setenv(ToolFailureRetryAttemptsEnvKey, strconv.FormatInt(int64(math.MaxInt32)+1, 10))
	fromEnv := toolFailureRetryOptionsFromConfig(nil)
	if fromEnv.MaxDelay != DefaultToolFailureRetryMaxDelay {
		t.Fatalf("env MaxDelay = %s, want the default %s", fromEnv.MaxDelay, DefaultToolFailureRetryMaxDelay)
	}
	if fromEnv.MaxAttempts != DefaultToolFailureRetryAttempts {
		t.Fatalf("env MaxAttempts = %d, want the default %d", fromEnv.MaxAttempts, DefaultToolFailureRetryAttempts)
	}
}

func TestResolveToolFailureRetryOptionsFromContext(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "")

	ctx := withToolFailureRetryOptions(context.Background(), ToolFailureRetryOptions{
		Enabled:     true,
		MaxAttempts: 6,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    20 * time.Millisecond,
	})
	opts := resolveToolFailureRetryOptions(ctx)
	if opts.MaxAttempts != 6 || opts.BaseDelay != 10*time.Millisecond {
		t.Fatalf("context policy not honoured: %+v", opts)
	}

	// No attached policy (eg a direct unit-test call) falls back to the
	// env/defaults instead of a zero policy.
	fallback := resolveToolFailureRetryOptions(context.Background())
	if fallback.MaxAttempts != DefaultToolFailureRetryAttempts || !fallback.Enabled {
		t.Fatalf("fallback policy = %+v, want the env/defaults", fallback)
	}
}

func TestToolFailureRetryWaitsStayBounded(t *testing.T) {
	for _, key := range []string{
		ToolFailureRetryBaseDelayEnvKey,
		ToolFailureRetryMaxDelayEnvKey,
		SandboxCapacityRetryBaseDelayEnvKey,
		SandboxCapacityRetryMaxDelayEnvKey,
	} {
		t.Setenv(key, "")
	}
	opts := toolFailureRetryOptionsFromConfig(nil)

	// The jitter stays within the documented bound of each curve, so a retry
	// wait can never exceed max + max/4 (blip) or max + max/2 (capacity).
	for attempt := 1; attempt <= 8; attempt++ {
		blip := toolFailureRetryWaitWithOptions(attempt, opts)
		if blip > opts.MaxDelay+opts.MaxDelay/4 {
			t.Fatalf("blip wait(attempt %d) = %s, above the bound", attempt, blip)
		}
		capacity := sandboxCapacityRetryWaitWithOptions(attempt, opts)
		if capacity > opts.CapacityMaxDelay+opts.CapacityMaxDelay/2 {
			t.Fatalf("capacity wait(attempt %d) = %s, above the bound", attempt, capacity)
		}
	}

	// A zero base disables the wait entirely (used by the tests/callers that
	// must not sleep).
	zero := toolFailureRetryOptionsFromConfig(map[string]interface{}{
		"tool_failure_retry_base_delay_ms":     0,
		"sandbox_capacity_retry_base_delay_ms": 0,
	})
	if got := toolFailureRetryWaitWithOptions(3, zero); got != 0 {
		t.Fatalf("blip wait with zero base = %s, want 0", got)
	}
	if got := sandboxCapacityRetryWaitWithOptions(3, zero); got != 0 {
		t.Fatalf("capacity wait with zero base = %s, want 0", got)
	}
}
