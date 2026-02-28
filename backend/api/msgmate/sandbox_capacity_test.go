package msgmate

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ci691SandboxCapacityError is the exact failure reported in ci#691: the
// Kubernetes scheduler could not place the sandbox pod because the node ran out
// of CPU, so the sandbox integration's credential re-provision path reports
//
//	docker sandbox lifecycle: failed to re-provision credentials for sandbox
//	ssh server <uuid> after 5m0s: credentials not ready for sandbox ssh server
//	<uuid>: sandbox "coder-7b205aa3-…" is not schedulable: Unschedulable:
//	0/1 nodes are available: 1 Insufficient cpu. ...
const ci691SandboxCapacityError = "coder tool api failed (400): failed to create temporary sandbox: " +
	`sandbox "coder-7b205aa3-142f-463e-b782" is not schedulable: Unschedulable: 0/1 nodes are available: ` +
	"1 Insufficient cpu. no new claims to deallocate, preemption: 0/1 nodes are available: " +
	"1 No preemption victims found for incoming pod."

// TestIsSandboxCapacityFailureRecognisesSchedulerRejection guards the marker
// list that decides whether a failure gets the slow capacity backoff and the
// fleet-wide provisioning cooldown (ci#691), and that an ordinary blip does
// not.
func TestIsSandboxCapacityFailureRecognisesSchedulerRejection(t *testing.T) {
	capacity := []error{
		errors.New(ci691SandboxCapacityError),
		errors.New("sandbox pod is not schedulable: insufficient memory"),
		errors.New("0/1 nodes are available: 1 Insufficient memory. no new preemption victims found."),
		errors.New("sandbox \"coder-x\" cannot start: Unschedulable"),
	}
	for _, err := range capacity {
		if !isSandboxCapacityFailure(err) {
			t.Fatalf("expected %v to be treated as a scheduler capacity failure", err)
		}
		if !isTransientToolFailure(err) {
			t.Fatalf("expected %v to stay a transient tool failure too", err)
		}
	}

	notCapacity := []error{
		nil,
		errors.New("copy-fd: Bad file descriptor"),
		errors.New("too many open files"),
		errors.New("dial tcp 10.0.0.5:22: connect: connection refused"),
		errors.New("file not found: internal/api/x.go"),
	}
	for _, err := range notCapacity {
		if isSandboxCapacityFailure(err) {
			t.Fatalf("expected %v to NOT be a scheduler capacity failure", err)
		}
	}
}

// TestSandboxCapacityBackoffDelayIsPatientAndCapped documents the capacity
// backoff shape: a linear ramp from the base (seconds, not milliseconds) capped
// at the max window.
func TestSandboxCapacityBackoffDelayIsPatientAndCapped(t *testing.T) {
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "5000")
	if got := sandboxCapacityBackoffDelay(1); got != 5*time.Second {
		t.Fatalf("expected 5s before the first capacity retry, got %s", got)
	}
	if got := sandboxCapacityBackoffDelay(2); got != 10*time.Second {
		t.Fatalf("expected 10s before the second capacity retry, got %s", got)
	}
	if got := sandboxCapacityBackoffDelay(20); got != DefaultSandboxCapacityRetryMaxDelay {
		t.Fatalf("expected the capacity delay to be capped at %s, got %s", DefaultSandboxCapacityRetryMaxDelay, got)
	}
	// The default is a seconds-scale wait, never the millisecond blip backoff.
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "")
	if got := sandboxCapacityBackoffDelay(1); got < time.Second {
		t.Fatalf("expected the default capacity backoff to be seconds-scale, got %s", got)
	}
}

// TestSandboxCapacityRetryWaitAddsBoundedJitter guards the de-synchronisation
// of the capacity retries: the wait never drops below the ramp and never grows
// past half a window above it (ci#691).
func TestSandboxCapacityRetryWaitAddsBoundedJitter(t *testing.T) {
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "5000")
	base := sandboxCapacityBackoffDelay(1)
	for i := 0; i < 200; i++ {
		got := sandboxCapacityRetryWait(1)
		if got < base || got > base+base/2 {
			t.Fatalf("expected the wait in [%s, %s], got %s", base, base+base/2, got)
		}
	}
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "0")
	if got := sandboxCapacityRetryWait(1); got != 0 {
		t.Fatalf("expected a zero wait when the base delay is zero, got %s", got)
	}
}

// TestRunToolWithTransientRetryUsesCapacityBackoff locks in the branch itself:
// a capacity rejection is retried with the seconds-scale capacity backoff while
// a plain blip keeps the millisecond backoff (ci#691).
func TestRunToolWithTransientRetryUsesCapacityBackoff(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "2")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")
	// A tiny capacity base keeps the test fast while still exercising the
	// dedicated branch.
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "1")

	runs := 0
	_, err, retries := runToolWithTransientRetry(context.Background(), "coder_select_workspace", func() (string, error) {
		runs++
		return "", errors.New(ci691SandboxCapacityError)
	})
	if err == nil {
		t.Fatal("expected the capacity failure to surface once attempts are exhausted")
	}
	if runs != 2 || retries != 1 {
		t.Fatalf("expected 2 attempts (1 retry), got runs=%d retries=%d", runs, retries)
	}
}

// TestRunToolWithTransientRetryCapacityRetryRecovers proves the capacity branch
// can absorb the condition it exists for: the second attempt succeeds.
func TestRunToolWithTransientRetryCapacityRetryRecovers(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "3")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "1")

	runs := 0
	result, err, retries := runToolWithTransientRetry(context.Background(), "coder_select_workspace", func() (string, error) {
		runs++
		if runs == 1 {
			return "", errors.New(ci691SandboxCapacityError)
		}
		return "sandbox ready", nil
	})
	if err != nil {
		t.Fatalf("expected the capacity failure to be retried away, got %v", err)
	}
	if result != "sandbox ready" || retries != 1 {
		t.Fatalf("expected the recovered result with 1 retry, got %q retries=%d", result, retries)
	}
}

// TestSandboxCapacityCooldownHoldsBackProvisioningCalls is the fleet-level
// guard: after a capacity rejection the next provisioning call waits for the
// cooldown, while a non-provisioning tool is never delayed (ci#691).
func TestSandboxCapacityCooldownHoldsBackProvisioningCalls(t *testing.T) {
	resetSandboxCapacityCooldown()
	t.Cleanup(resetSandboxCapacityCooldown)

	if got := awaitSandboxCapacityCooldown(context.Background(), "coder_read"); got != 0 {
		t.Fatalf("expected no wait without a cooldown, got %s", got)
	}

	noteSandboxCapacityFailure(40 * time.Millisecond)
	if remaining := sandboxCapacityCooldownRemaining(); remaining <= 0 {
		t.Fatal("expected an active capacity cooldown")
	}
	if got := awaitSandboxCapacityCooldown(context.Background(), "coder_list"); got <= 0 {
		t.Fatal("expected a provisioning call to be held back during the cooldown")
	}
	// A non-provisioning tool is never held back, even during a cooldown.
	if got := awaitSandboxCapacityCooldown(context.Background(), "web_search"); got != 0 {
		t.Fatalf("expected a non-provisioning tool to skip the cooldown, got %s", got)
	}
	// The cooldown is a wait, not a permanent block.
	if remaining := sandboxCapacityCooldownRemaining(); remaining > 0 {
		t.Fatalf("expected the cooldown to have elapsed, %s remaining", remaining)
	}
}

// TestSandboxCapacityCooldownHonoursCancellation guards the stop button: a
// cancelled generation context ends the cooldown wait immediately (ci#691).
func TestSandboxCapacityCooldownHonoursCancellation(t *testing.T) {
	resetSandboxCapacityCooldown()
	t.Cleanup(resetSandboxCapacityCooldown)

	noteSandboxCapacityFailure(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if got := awaitSandboxCapacityCooldown(ctx, "coder_select_workspace"); got > time.Second {
		t.Fatalf("expected the cancelled cooldown wait to return immediately, waited %s", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected cancellation to end the wait, took %s", elapsed)
	}
}

// TestBuildSandboxCapacityToolErrorPlaceholderExplainsCapacity guards the
// model-facing wording: it must say the failure is cluster capacity, that it is
// budget-exempt, and that the agent should wait instead of re-provisioning in a
// loop (ci#691).
func TestBuildSandboxCapacityToolErrorPlaceholderExplainsCapacity(t *testing.T) {
	got := buildSandboxCapacityToolErrorPlaceholder("coder_select_workspace", errors.New(ci691SandboxCapacityError), 3)
	for _, fragment := range []string{
		"coder_select_workspace",
		"out of capacity",
		"after 3 attempt(s)",
		"Insufficient cpu",
		"does NOT count against your failed-tool budget",
		"wait a little before provisioning again",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("expected %q to contain %q", got, fragment)
		}
	}
}

// TestSandboxCapacityFailureCountCountsOnlyCapacityFailures guards the
// reporting rule: only a failed call flagged as a capacity rejection is counted
// (ci#691).
func TestSandboxCapacityFailureCountCountsOnlyCapacityFailures(t *testing.T) {
	calls := []executedToolCall{
		{name: "coder_read", status: ToolCallStatusSucceeded},
		{name: "coder_list", status: ToolCallStatusFailed, budgetExempt: true, retries: 2},
		{name: "coder_select_workspace", status: ToolCallStatusFailed, budgetExempt: true, sandboxCapacity: true, retries: 2},
		{name: "coder_git", status: ToolCallStatusPendingConfirmation},
	}
	if got := sandboxCapacityFailureCount(calls); got != 1 {
		t.Fatalf("expected exactly 1 capacity failure, got %d", got)
	}
	failed, transient := toolCallFailureAccounting(calls)
	if failed != 0 || transient != 2 {
		t.Fatalf("expected both failures to stay budget-exempt (failed=%d transient=%d)", failed, transient)
	}
}

// TestProcessStreamingResponseReaderMarksSandboxCapacityFailure proves the
// ci#691 wiring end to end for one round: a tool that fails with the scheduler
// rejection is reported as failed, budget-exempt, marked as a capacity failure
// and reaches the model with the capacity guidance.
func TestProcessStreamingResponseReaderMarksSandboxCapacityFailure(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "2")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")
	t.Setenv(SandboxCapacityRetryBaseDelayEnvKey, "1")
	resetSandboxCapacityCooldown()
	t.Cleanup(resetSandboxCapacityCooldown)

	tool := &flakyTool{
		BaseTool:     &BaseTool{ToolName: "coder_select_workspace"},
		failuresLeft: 5,
		errMsg:       ci691SandboxCapacityError,
	}
	toolMap := map[string]Tool{"coder_select_workspace": tool}

	sse := sseFromToolCallChunks([]map[string]interface{}{
		toolCallChunk("call-1", "coder_select_workspace", `{}`),
	})

	chunkChan := make(chan string, 8)
	usageChan := make(chan *TokenUsage, 2)
	toolChan := make(chan ToolCall, 8)

	result, err := processStreamingResponseReader(
		context.Background(),
		bufio.NewReader(strings.NewReader(sse)),
		toolMap,
		map[string]string{},
		chunkChan,
		usageChan,
		toolChan,
	)
	if err != nil {
		t.Fatalf("stream processing failed: %v", err)
	}
	if len(result.calls) != 1 {
		t.Fatalf("expected one executed call, got %d", len(result.calls))
	}
	call := result.calls[0]
	if call.status != ToolCallStatusFailed {
		t.Fatalf("expected the call to be failed, got %q", call.status)
	}
	if !call.budgetExempt || !call.sandboxCapacity {
		t.Fatalf("expected the capacity failure to be budget-exempt and marked, got %+v", call)
	}
	if !strings.Contains(call.result, "out of capacity") {
		t.Fatalf("expected the model-facing result to explain the capacity condition, got %q", call.result)
	}
	if got := sandboxCapacityFailureCount(result.calls); got != 1 {
		t.Fatalf("expected 1 capacity failure, got %d", got)
	}
	if remaining := sandboxCapacityCooldownRemaining(); remaining <= 0 {
		t.Fatal("expected the failed capacity provision to start the fleet-wide cooldown")
	}

	close(toolChan)
	var failedFrames []ToolCall
	for frame := range toolChan {
		if frame.Status == ToolCallStatusFailed {
			failedFrames = append(failedFrames, frame)
		}
	}
	if len(failedFrames) != 1 {
		t.Fatalf("expected one failed frame, got %d", len(failedFrames))
	}
	if !failedFrames[0].SandboxCapacity || !failedFrames[0].BudgetExempt {
		t.Fatalf("expected the frame to carry the capacity marker, got %+v", failedFrames[0])
	}
}
