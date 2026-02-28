package msgmate

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// This file owns the *scheduler capacity* half of the transient-tool-failure
// policy (ci#691). ci#688 taught the tool loop to retry and budget-exempt
// environmental failures, but it backed every one of them off on a
// millisecond scale. That is right for a connection blip or a file-descriptor
// exhaustion and wrong for a Kubernetes scheduling rejection:
//
//	sandbox "coder-7b205aa3-…" is not schedulable: Unschedulable:
//	0/1 nodes are available: 1 Insufficient cpu. no new claims to deallocate,
//	preemption: 0/1 nodes are available: 1 No preemption victims found for
//	incoming pod.
//
// A node that has run out of requests only frees them when a finished sandbox
// is reaped or a peer agent releases its pod — tens of seconds, not hundreds of
// milliseconds. Retrying three times within ~1.2s therefore burned the attempt
// budget on a condition that could not have changed, and every agent of a
// saturated pool did it at the same time, which is exactly how the burst that
// saturated the node in the first place is sustained (the same finding the
// sandbox integration's background credential re-provision path reports, see
// msgmate-io/open-chat-go-docker-sandbox-integration#29 and ci#529).
//
// The policy here is therefore:
//
//  1. a capacity rejection is retried with the slower, bounded capacity backoff
//     of tool_failure_retry.go (5s → 30s, linear, plus bounded jitter) instead
//     of the millisecond blip backoff;
//  2. while the node is known to be saturated, the *other* provisioning calls
//     of the same agent fleet are held back for the same bounded window, so the
//     loop does not stampede the scheduler with a fresh provision attempt per
//     tool call (the cooldown is node-scoped, not chat-scoped: capacity is a
//     property of the node, not of one interaction);
//  3. the failure that reaches the model says what actually happened — the
//     sandbox could not be *scheduled* because the node is out of CPU/memory —
//     so the agent waits or continues with work that needs no sandbox instead
//     of re-cloning in a loop.

// coderProvisioningToolPrefix is the tool-name prefix of the coder tools that
// can provision a per-chat sandbox as a side effect (coder_select_workspace,
// coder_list, coder_read, coder_git, ... all route through the same workspace
// provisioning path, which is why the ci#691 log shows several different tool
// names for one capacity problem).
const coderProvisioningToolPrefix = "coder_"

// isSandboxProvisioningTool reports whether a tool call may provision a per-chat
// coding sandbox, ie whether a node-capacity cooldown applies to it.
func isSandboxProvisioningTool(toolName string) bool {
	return strings.HasPrefix(strings.TrimSpace(toolName), coderProvisioningToolPrefix)
}

var (
	// sandboxCapacityMu guards the process-wide capacity cooldown. The cooldown
	// is deliberately process-wide: node capacity is shared by every interaction
	// served by this backend, so one agent learning that the node is saturated
	// is information the whole fleet can use.
	sandboxCapacityMu sync.Mutex
	// sandboxCapacityUntil is the instant until which new sandbox provisioning
	// attempts are held back (zero means "no cooldown").
	sandboxCapacityUntil time.Time
)

// noteSandboxCapacityFailure records that the scheduler rejected a sandbox for
// capacity and starts (or extends) the process-wide provisioning cooldown by
// window. A non-positive window is ignored.
func noteSandboxCapacityFailure(window time.Duration) {
	if window <= 0 {
		return
	}
	until := time.Now().Add(window)
	sandboxCapacityMu.Lock()
	defer sandboxCapacityMu.Unlock()
	if until.After(sandboxCapacityUntil) {
		sandboxCapacityUntil = until
	}
}

// sandboxCapacityCooldownRemaining reports how long a new provisioning attempt
// is still held back. Zero means "attempt now".
func sandboxCapacityCooldownRemaining() time.Duration {
	sandboxCapacityMu.Lock()
	defer sandboxCapacityMu.Unlock()
	if sandboxCapacityUntil.IsZero() {
		return 0
	}
	remaining := time.Until(sandboxCapacityUntil)
	if remaining <= 0 {
		sandboxCapacityUntil = time.Time{}
		return 0
	}
	return remaining
}

// resetSandboxCapacityCooldown clears the cooldown. It exists for tests, which
// must not leak a cooldown into the next test.
func resetSandboxCapacityCooldown() {
	sandboxCapacityMu.Lock()
	defer sandboxCapacityMu.Unlock()
	sandboxCapacityUntil = time.Time{}
}

// awaitSandboxCapacityCooldown holds a provisioning tool call back until the
// process-wide capacity cooldown elapsed, and reports how long it waited. It is
// a no-op for a non-provisioning tool and when no cooldown is active. The
// generation context bounds the wait, so a user stop is never resurrected by
// the cooldown.
func awaitSandboxCapacityCooldown(ctx context.Context, toolName string) time.Duration {
	if !isSandboxProvisioningTool(toolName) {
		return 0
	}
	remaining := sandboxCapacityCooldownRemaining()
	if remaining <= 0 {
		return 0
	}
	// Never wait longer than one capacity window; a stale cooldown must not
	// stall a turn whose node has since recovered.
	if remaining > DefaultSandboxCapacityRetryMaxDelay {
		remaining = DefaultSandboxCapacityRetryMaxDelay
	}
	log.Printf(
		"Tool %s held back for %s: the node rejected a sandbox for capacity; avoiding a provisioning stampede (ci#691)",
		toolName, remaining,
	)
	start := time.Now()
	select {
	case <-ctx.Done():
		return time.Since(start)
	case <-time.After(remaining):
	}
	return time.Since(start)
}

// sandboxCapacityFailureCount counts the failed calls of one round whose sandbox
// the scheduler rejected for node capacity (ci#691). It is the capacity-specific
// subset of toolCallFailureAccounting's transient failures, reported separately
// so a saturated node is visible in the transcript and in the
// interaction_complete payload.
func sandboxCapacityFailureCount(calls []executedToolCall) int {
	count := 0
	for _, call := range calls {
		if call.status == ToolCallStatusFailed && call.sandboxCapacity {
			count++
		}
	}
	return count
}

// buildSandboxCapacityToolErrorPlaceholder builds the model-facing result of a
// provisioning call whose sandbox could not be *scheduled* even after the
// capacity retries (ci#691). It tells the model what it cannot infer from the
// bare error: the failure is node capacity, not its arguments, it did not
// consume the failed-tool budget, and the right response is to wait (the pool
// frees capacity as other sandboxes finish) or to continue with work that needs
// no sandbox — never to immediately re-provision in a loop.
func buildSandboxCapacityToolErrorPlaceholder(toolName string, runErr error, attempts int) string {
	reason := "unknown error"
	if runErr != nil {
		reason = runErr.Error()
	}
	return fmt.Sprintf(
		"Tool %s could not get a coding sandbox: the node is out of capacity after %d attempt(s) (%s). "+
			"This is a cluster capacity condition, not caused by your arguments, and it does NOT count against your failed-tool budget. "+
			"The pool frees capacity as other sandboxes finish, so wait a little before provisioning again "+
			"and prefer work that needs no sandbox (or continue from the work already done) instead of retrying immediately.",
		toolName,
		attempts,
		reason,
	)
}
