package msgmate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ci688SandboxError is the exact failure reported in ci#688: a per-chat
// sandbox could not be provisioned under high parallelism because the local
// clone ran out of file descriptors.
const ci688SandboxError = "coder tool api failed (400): failed to create temporary sandbox: " +
	"Cloning into '/tmp/msgmate-coder/sandbox-abc/open-chat-go'... error: copy-fd: Bad file descriptor"

// flakyTool fails with errMsg for the first failuresLeft runs and succeeds
// afterwards, so a test can prove the retry loop recovers a blip.
type flakyTool struct {
	*BaseTool
	failuresLeft int
	runs         int
	errMsg       string
}

func (t *flakyTool) RunTool(input interface{}) (string, error) {
	t.runs++
	if t.failuresLeft > 0 {
		t.failuresLeft--
		return "", errors.New(t.errMsg)
	}
	return "recovered", nil
}

// TestIsTransientToolFailureRecognisesSandboxProvisioning guards the marker list
// that decides whether a failed tool call may be retried and exempted from the
// agent's failed-tool budget (ci#688).
func TestIsTransientToolFailureRecognisesSandboxProvisioning(t *testing.T) {
	transient := []error{
		errors.New(ci688SandboxError),
		errors.New("failed to provision temporary sandbox for chat x"),
		errors.New("copy-fd: Bad file descriptor"),
		errors.New("too many open files"),
		errors.New("no space left on device"),
		errors.New("dial tcp 10.0.0.5:22: connect: connection refused"),
		errors.New("sandbox pod is not schedulable: insufficient memory"),
		errors.New("unexpected EOF"),
	}
	for _, err := range transient {
		if !isTransientToolFailure(err) {
			t.Fatalf("expected %v to be treated as a transient tool failure", err)
		}
	}

	notTransient := []error{
		nil,
		errors.New("no workspace selected for this chat; call coder_select_workspace first"),
		errors.New("file not found: internal/api/x.go"),
		errors.New("permission denied"),
		errors.New("invalid arguments: path is required"),
		errors.New("compilation failed: undefined: foo"),
	}
	for _, err := range notTransient {
		if isTransientToolFailure(err) {
			t.Fatalf("expected %v to NOT be treated as a transient tool failure", err)
		}
	}
}

// TestSandboxRegistryConflictIsNotTransient locks in ci#739: a deterministic
// sandbox-registry constraint violation must not enter the transient retry loop.
// The derived sandbox name is chat-scoped, so a stale row collides identically on
// every attempt — retrying only burns the turn budget.
func TestSandboxRegistryConflictIsNotTransient(t *testing.T) {
	conflicts := []error{
		errors.New("failed creating chat sandbox: constraint failed: UNIQUE constraint failed: docker_sandboxes.owner_user_id, docker_sandboxes.name (2067)"),
		errors.New("pq: duplicate key value violates unique constraint \"docker_sandboxes_name_owner\""),
		errors.New("UNIQUE constraint failed"),
	}
	for _, err := range conflicts {
		if isTransientToolFailure(err) {
			t.Fatalf("expected %v to NOT be treated as a transient tool failure", err)
		}
		if !isSandboxRegistryConflict(err) {
			t.Fatalf("expected %v to be detected as a sandbox registry conflict", err)
		}
	}
	if isSandboxRegistryConflict(nil) {
		t.Fatal("a nil error is never a registry conflict")
	}
	if isSandboxRegistryConflict(errors.New("connection refused")) {
		t.Fatal("a connection blip is not a registry conflict")
	}
}

// TestRunToolWithTransientRetryDoesNotRetryRegistryConflict pins the bound: a
// registry conflict fails on the first attempt instead of consuming the retry
// budget (ci#739).
func TestRunToolWithTransientRetryDoesNotRetryRegistryConflict(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "3")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	runs := 0
	_, err, retries := runToolWithTransientRetry(context.Background(), "coder_select_workspace", func() (string, error) {
		runs++
		return "", errors.New("failed creating chat sandbox: constraint failed: UNIQUE constraint failed: docker_sandboxes.owner_user_id, docker_sandboxes.name (2067)")
	})
	if err == nil {
		t.Fatal("expected the registry conflict to surface")
	}
	if runs != 1 || retries != 0 {
		t.Fatalf("expected a single attempt, got runs=%d retries=%d", runs, retries)
	}
}

// TestRunToolWithTransientRetryRecovers locks in that a transient blip is
// absorbed by the retry instead of surfacing as a hard failure.
func TestRunToolWithTransientRetryRecovers(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "3")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	runs := 0
	result, err, retries := runToolWithTransientRetry(context.Background(), "coder_select_workspace", func() (string, error) {
		runs++
		if runs < 3 {
			return "", errors.New(ci688SandboxError)
		}
		return "sandbox ready", nil
	})
	if err != nil {
		t.Fatalf("expected the transient failure to be retried away, got %v", err)
	}
	if result != "sandbox ready" {
		t.Fatalf("expected the successful result, got %q", result)
	}
	if runs != 3 || retries != 2 {
		t.Fatalf("expected 3 attempts (2 retries), got runs=%d retries=%d", runs, retries)
	}
}

// TestRunToolWithTransientRetryGivesUpAfterAttempts checks the bound: an
// exhausted retry budget returns the last error (and the real attempt count).
func TestRunToolWithTransientRetryGivesUpAfterAttempts(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "2")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	runs := 0
	_, err, retries := runToolWithTransientRetry(context.Background(), "coder_select_workspace", func() (string, error) {
		runs++
		return "", errors.New(ci688SandboxError)
	})
	if err == nil {
		t.Fatal("expected the transient failure to surface once attempts are exhausted")
	}
	if runs != 2 || retries != 1 {
		t.Fatalf("expected 2 attempts (1 retry), got runs=%d retries=%d", runs, retries)
	}
}

// TestRunToolWithTransientRetryNeverRetriesModelErrors is the important half:
// a failure the model can fix must not be retried, so the retry policy never
// masks a real tool/argument error.
func TestRunToolWithTransientRetryNeverRetriesModelErrors(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "5")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	runs := 0
	_, err, retries := runToolWithTransientRetry(context.Background(), "coder_read", func() (string, error) {
		runs++
		return "", errors.New("file not found: internal/api/x.go")
	})
	if err == nil {
		t.Fatal("expected the non-transient failure to surface")
	}
	if runs != 1 || retries != 0 {
		t.Fatalf("expected exactly one attempt for a non-transient failure, got runs=%d retries=%d", runs, retries)
	}
}

// TestRunToolWithTransientRetryStopsOnCancelledContext guards the stop button:
// a cancelled generation context must never be resurrected by the retry loop.
func TestRunToolWithTransientRetryStopsOnCancelledContext(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "5")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	_, err, _ := runToolWithTransientRetry(ctx, "coder_select_workspace", func() (string, error) {
		runs++
		cancel()
		return "", errors.New(ci688SandboxError)
	})
	if err == nil {
		t.Fatal("expected the failure to surface after cancellation")
	}
	if runs != 1 {
		t.Fatalf("expected no retry after cancellation, got %d runs", runs)
	}
}

// TestToolFailureBackoffDelayGrowsAndIsCapped documents the backoff shape.
func TestToolFailureBackoffDelayGrowsAndIsCapped(t *testing.T) {
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "100")
	if got := toolFailureBackoffDelay(1); got != 100*time.Millisecond {
		t.Fatalf("expected 100ms before the first retry, got %s", got)
	}
	if got := toolFailureBackoffDelay(2); got != 200*time.Millisecond {
		t.Fatalf("expected 200ms before the second retry, got %s", got)
	}
	if got := toolFailureBackoffDelay(20); got != DefaultToolFailureRetryMaxDelay {
		t.Fatalf("expected the delay to be capped at %s, got %s", DefaultToolFailureRetryMaxDelay, got)
	}
}

// TestToolFailureRetryWaitAddsBoundedJitter guards the de-synchronisation of
// retries: the wait is never below the deterministic backoff and never more
// than a quarter above it, so a saturated sandbox pool does not retry in
// lockstep without the wait becoming unpredictable (ci#688).
func TestToolFailureRetryWaitAddsBoundedJitter(t *testing.T) {
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "400")
	base := toolFailureBackoffDelay(1)
	for i := 0; i < 200; i++ {
		got := toolFailureRetryWait(1)
		if got < base || got > base+base/4 {
			t.Fatalf("expected the wait in [%s, %s], got %s", base, base+base/4, got)
		}
	}
	if got := toolFailureRetryWait(1); got == 0 {
		t.Fatal("expected a non-zero wait for a non-zero backoff")
	}
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")
	if got := toolFailureRetryWait(1); got != 0 {
		t.Fatalf("expected a zero wait when the base delay is zero, got %s", got)
	}
}

// TestToolCallFailureAccountingExemptsTransientFailures is the budget rule: a
// transient environmental failure does not consume the failed-tool budget,
// while a model-fixable failure does.
func TestToolCallFailureAccountingExemptsTransientFailures(t *testing.T) {
	calls := []executedToolCall{
		{name: "coder_read", status: ToolCallStatusSucceeded},
		{name: "coder_read", status: ToolCallStatusFailed},
		{name: "coder_select_workspace", status: ToolCallStatusFailed, budgetExempt: true, retries: 2},
		{name: "coder_bash", status: ToolCallStatusPendingConfirmation},
	}
	failed, transient := toolCallFailureAccounting(calls)
	if failed != 1 {
		t.Fatalf("expected 1 budget-consuming failure, got %d", failed)
	}
	if transient != 1 {
		t.Fatalf("expected 1 budget-exempt transient failure, got %d", transient)
	}
}

// TestProcessStreamingResponseReaderMarksTransientFailureBudgetExempt proves the
// wiring end to end for one round: a tool that fails with the ci#688 sandbox
// error is reported as failed *and* marked budget-exempt (with its retry
// count), and the same flags reach the ToolCall frame the UI reads.
func TestProcessStreamingResponseReaderMarksTransientFailureBudgetExempt(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "2")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	tool := &flakyTool{
		BaseTool:     &BaseTool{ToolName: "coder_select_workspace"},
		failuresLeft: 5,
		errMsg:       ci688SandboxError,
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
	if !call.budgetExempt {
		t.Fatal("expected the transient sandbox failure to be exempt from the failed-tool budget")
	}
	if call.retries != 1 {
		t.Fatalf("expected 1 retry to be recorded, got %d", call.retries)
	}
	if !strings.Contains(call.result, "does NOT count against your failed-tool budget") {
		t.Fatalf("expected the model-facing result to explain the exemption, got %q", call.result)
	}
	if got := tool.runs; got != 2 {
		t.Fatalf("expected the tool body to be attempted twice, got %d", got)
	}
	failed, transient := toolCallFailureAccounting(result.calls)
	if failed != 0 || transient != 1 {
		t.Fatalf("expected the failure to be budget-exempt only, got failed=%d transient=%d", failed, transient)
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
	if !failedFrames[0].BudgetExempt || failedFrames[0].Retries != 1 {
		t.Fatalf("expected the frame to carry the exemption and retry count, got %+v", failedFrames[0])
	}
}

// TestBuildTransientToolErrorPlaceholderExplainsRecovery guards the model-facing
// wording: it must say the failure is environmental, budget-exempt, and worth a
// short-wait retry.
func TestBuildTransientToolErrorPlaceholderExplainsRecovery(t *testing.T) {
	got := buildTransientToolErrorPlaceholder("coder_select_workspace", errors.New(ci688SandboxError), 3)
	for _, fragment := range []string{
		"coder_select_workspace",
		"after 3 attempt(s)",
		"copy-fd",
		"does NOT count against your failed-tool budget",
		"Retry the call after a short wait",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("expected %q to contain %q", got, fragment)
		}
	}
}

// sandboxFailureSSEServer answers every provider request with a single tool
// call for `toolName`, so the tool loop keeps running the tool. Each round gets
// a unique call id (the tool loop attributes calls by id), so every round is a
// fresh call rather than a duplicate of the previous one.
func sandboxFailureSSEServer(t *testing.T, toolName string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		payload := sseFromToolCallChunks([]map[string]interface{}{
			toolCallChunk(fmt.Sprintf("call-%d", round), toolName, `{}`),
		})
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// drainStreamChatCompletion consumes the channels of a streamChatCompletion
// invocation and returns the final error (nil when the turn finished cleanly).
func drainStreamChatCompletion(chunks <-chan string, usage <-chan *TokenUsage, toolCalls <-chan ToolCall, errs <-chan error) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range chunks {
		}
	}()
	go func() {
		for range usage {
		}
	}()
	go func() {
		for range toolCalls {
		}
	}()
	err := <-errs
	<-done
	return err
}

// TestTransientToolFailureDoesNotConsumeFailedToolBudget is the ci#688
// end-to-end regression: with a failed-tool budget of 1, a tool that keeps
// failing for the transient sandbox reason must NOT abort the turn with
// "exceeded maximum number of failed tool calls". The turn still ends — the
// total tool-call cap keeps it bounded — but it ends on the *total* cap.
func TestTransientToolFailureDoesNotConsumeFailedToolBudget(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "1")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	// A fresh tool instance per round: the failure must stay transient and
	// non-recoverable so the budget rule is what is measured.
	failForever := errors.New(ci688SandboxError)
	toolMap := map[string]Tool{
		"coder_select_workspace": &failingTool{BaseTool: &BaseTool{ToolName: "coder_select_workspace"}, err: failForever},
	}

	server, requests := sandboxFailureSSEServer(t, "coder_select_workspace")

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm",
		3, // tool_call_max_total
		1, // tool_call_max_failed
		[]map[string]interface{}{{"role": "user", "content": "select a workspace"}},
		nil, toolMap, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-1"},
	)

	err := drainStreamChatCompletion(chunks, usage, toolCalls, errs)
	if err == nil {
		t.Fatal("expected the turn to end on the total tool-call cap")
	}
	if strings.Contains(err.Error(), "exceeded maximum number of failed tool calls") {
		t.Fatalf("a transient sandbox failure consumed the failed-tool budget: %v", err)
	}
	if !strings.Contains(err.Error(), "exceeded maximum number of tool calls") {
		t.Fatalf("expected the total tool-call cap to bound the turn, got %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("expected 3 provider rounds (the total cap), got %d", got)
	}
}

// TestModelFixableToolFailureStillConsumesFailedToolBudget is the counterpart:
// the budget exemption must not weaken the guard against a tool that genuinely
// keeps failing (a bad argument the model repeats).
func TestModelFixableToolFailureStillConsumesFailedToolBudget(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "3")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	toolMap := map[string]Tool{
		"coder_read": &failingTool{BaseTool: &BaseTool{ToolName: "coder_read"}, err: errors.New("file not found: internal/api/x.go")},
	}

	server, requests := sandboxFailureSSEServer(t, "coder_read")

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm",
		10, // tool_call_max_total: high enough to never trigger
		1,  // tool_call_max_failed
		[]map[string]interface{}{{"role": "user", "content": "read a file"}},
		nil, toolMap, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-1"},
	)

	err := drainStreamChatCompletion(chunks, usage, toolCalls, errs)
	if err == nil {
		t.Fatal("expected the turn to abort on the failed-tool budget")
	}
	if !strings.Contains(err.Error(), "exceeded maximum number of failed tool calls") {
		t.Fatalf("expected a model-fixable failure to consume the failed-tool budget, got %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected a non-transient failure to be attempted without retries, got %d provider rounds", got)
	}
}

// ci739RegistryConflictError is the exact failure reported in ci#739: a stale
// sandbox row makes the chat-scoped sandbox name collide on every attempt.
const ci739RegistryConflictError = "failed creating chat sandbox: constraint failed: " +
	"UNIQUE constraint failed: docker_sandboxes.owner_user_id, docker_sandboxes.name (2067)"

// TestSandboxRegistryConflictPlaceholderNamesTheRemedy guards the model-facing
// wording of a registry conflict (ci#739): it must name the class, the reason
// and the remedies, and it must *not* be sold as budget-exempt — a conflict is
// deterministic, so calling it free would invite the identical, always-colliding
// retry the whole policy exists to prevent.
func TestSandboxRegistryConflictPlaceholderNamesTheRemedy(t *testing.T) {
	got := buildSandboxRegistryConflictToolErrorPlaceholder(
		"coder_select_workspace",
		errors.New(ci739RegistryConflictError),
	)
	for _, fragment := range []string{
		"coder_select_workspace",
		"coding-sandbox registry constraint",
		"docker_sandboxes.name",
		"not retried",
		"DOES count against your failed-tool budget",
		"Do not re-issue the same call",
		"workspace_uuid",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("expected %q to contain %q", got, fragment)
		}
	}
	if strings.Contains(got, "does NOT count against your failed-tool budget") {
		t.Fatalf("a deterministic registry conflict must never be reported as budget-exempt, got %q", got)
	}
	if !strings.Contains(
		buildSandboxRegistryConflictToolErrorPlaceholder("coder_select_workspace", nil),
		"unknown registry constraint",
	) {
		t.Fatal("expected a nil error to fall back to the generic constraint reason")
	}
}

// TestRegistryConflictConsumesFailedToolBudget is the ci#739 counterpart of the
// ci#688 exemption: a registry conflict is deterministic and the model *can*
// change course (another sandbox/workspace), so it stays a counted failure. With
// a failed-tool budget of 1 the very first conflict must abort the turn and the
// call must carry no retries — the retry would be a byte-identical collision.
func TestRegistryConflictConsumesFailedToolBudget(t *testing.T) {
	t.Setenv(ToolFailureRetryAttemptsEnvKey, "3")
	t.Setenv(ToolFailureRetryBaseDelayEnvKey, "0")

	toolMap := map[string]Tool{
		"coder_select_workspace": &failingTool{
			BaseTool: &BaseTool{ToolName: "coder_select_workspace"},
			err:      errors.New(ci739RegistryConflictError),
		},
	}

	server, requests := sandboxFailureSSEServer(t, "coder_select_workspace")

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm",
		10, // tool_call_max_total: high enough to never trigger
		1,  // tool_call_max_failed
		[]map[string]interface{}{{"role": "user", "content": "select a workspace"}},
		nil, toolMap, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-1"},
	)

	collected := make(chan struct{})
	var mu sync.Mutex
	var frames []ToolCall
	go func() {
		defer close(collected)
		for frame := range toolCalls {
			mu.Lock()
			frames = append(frames, frame)
			mu.Unlock()
		}
	}()
	go func() {
		for range chunks {
		}
	}()
	go func() {
		for range usage {
		}
	}()
	err := <-errs
	// The error channel is closed *before* the tool-call channel, so waiting on
	// the collector is what guarantees every frame of this turn was recorded.
	<-collected

	if err == nil {
		t.Fatal("expected the turn to abort on the failed-tool budget")
	}
	if !strings.Contains(err.Error(), "exceeded maximum number of failed tool calls") {
		t.Fatalf("expected a registry conflict to consume the failed-tool budget, got %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected the conflict to abort in a single provider round, got %d", got)
	}

	mu.Lock()
	turnFrames := append([]ToolCall(nil), frames...)
	mu.Unlock()

	var failedFrames []ToolCall
	for _, frame := range turnFrames {
		if frame.Status == ToolCallStatusFailed {
			failedFrames = append(failedFrames, frame)
		}
	}
	if len(failedFrames) != 1 {
		t.Fatalf("expected one failed frame, got %d", len(failedFrames))
	}
	if failedFrames[0].Retries != 0 {
		t.Fatalf("expected no retry for a deterministic conflict, got %d", failedFrames[0].Retries)
	}
	if failedFrames[0].BudgetExempt {
		t.Fatal("expected a registry conflict to stay inside the failed-tool budget")
	}
	if !strings.Contains(failedFrames[0].Result, "registry constraint") {
		t.Fatalf("expected the frame to carry the remedy, got %q", failedFrames[0].Result)
	}
}
