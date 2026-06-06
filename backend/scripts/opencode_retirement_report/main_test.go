package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"backend/nativecoder"
)

// TestFlagsFirstKeepsFlagsBeforePositionals pins the argument reordering that
// lets the documented `job state.json --format=json` invocation work with the
// stdlib flag package (which stops parsing at the first positional argument).
func TestFlagsFirstKeepsFlagsBeforePositionals(t *testing.T) {
	got := flagsFirst([]string{"state.json", "--format=json", "-", "--require-ready"})
	want := []string{"--format=json", "--require-ready", "state.json", "-"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("flagsFirst = %v, want %v", got, want)
	}
}

// TestReportRendersPerStepEvidence runs the built job against a blocked and a
// ready state, so the CLI and the nativecoder gate stay wired together.
func TestReportRendersPerStepEvidence(t *testing.T) {
	binary := buildJob(t)

	blocked, code := runJob(t, binary, map[string]any{}, "--format=json", "--require-ready")
	if code == 0 {
		t.Fatalf("expected a non-zero exit for a blocked gate, got 0:\n%s", blocked)
	}
	var rep report
	if err := json.Unmarshal([]byte(blocked), &rep); err != nil {
		t.Fatalf("decode report: %v\n%s", err, blocked)
	}
	if rep.Ready {
		t.Fatalf("blocked state reported ready: %+v", rep)
	}
	if len(rep.Steps) != len(nativecoder.OpencodeRetirementPlan()) || len(rep.Blockers) == 0 {
		t.Fatalf("blocked state must carry one step per checklist entry and blockers: %+v", rep)
	}
	for _, step := range rep.Steps {
		if step.ID == "" || step.Ticket == "" || step.Status == "" || len(step.Detail) == 0 {
			t.Fatalf("every blocked step needs id/ticket/status/reason, got %+v", step)
		}
	}

	markdown, code := runJob(t, binary, readyState(), "--require-ready")
	if code != 0 {
		t.Fatalf("fully native state must exit 0, got %d:\n%s", code, markdown)
	}
	if !strings.Contains(markdown, "| done |") {
		t.Fatalf("markdown must report a done step:\n%s", markdown)
	}
	if !strings.Contains(blocked, `"pending_flip"`) {
		t.Fatalf("the JSON report must carry the ci#582 flip delta:\n%s", blocked)
	}
}

// TestReportRendersTheFlipDelta covers the vault-sync half of the umbrella: the
// job prints the ci#582 flip delta and how much of it is still outstanding.
func TestReportRendersTheFlipDelta(t *testing.T) {
	binary := buildJob(t)

	delta, code := runJob(t, binary, map[string]any{}, "--format=delta")
	if code != 0 {
		t.Fatalf("--format=delta must succeed, got %d:\n%s", code, delta)
	}
	for _, want := range []string{"| family | bot |", "publish-open-chat-config.yaml", "DRY_RUN=true", "still outstanding for the observed fleet"} {
		if !strings.Contains(delta, want) {
			t.Errorf("delta output is missing %q:\n%s", want, delta)
		}
	}
	if !strings.Contains(delta, "still outstanding for the observed fleet: ") {
		t.Fatalf("delta output must report the outstanding entries:\n%s", delta)
	}

	fullyNative, code := runJob(t, binary, readyState(), "--format=delta")
	if code != 0 {
		t.Fatalf("--format=delta on a native fleet must succeed, got %d:\n%s", code, fullyNative)
	}
	if !strings.Contains(fullyNative, "0 of ") {
		t.Fatalf("a native fleet must have no outstanding flip entries:\n%s", fullyNative)
	}
}

// readyState is the fully flipped fleet expressed the way the job reads it.
func readyState() map[string]any {
	runtimes := map[string]string{}
	var stages []string
	for _, stage := range nativecoder.NativeBotStages() {
		runtimes[stage.NativeRuntime] = stage.NativeRuntime
		stages = append(stages, stage.Stage)
	}
	return map[string]any{
		"configured_runtimes": runtimes,
		"rolled_out_through":  stages[len(stages)-1],
		"rollback_proven_on":  stages[0],
		"native_stable_days":  nativecoder.NativeBotStabilityWindowDays,
		"parity_signed_off":   true,
		"ab_validation_done":  true,
		"retirement_delta_ok": true,
	}
}

func buildJob(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "opencode_retirement_report")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build job: %v\n%s", err, out)
	}
	return binary
}

func runJob(t *testing.T, binary string, state map[string]any, args ...string) (string, int) {
	t.Helper()
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run job: %v", err)
		}
		code = exit.ExitCode()
	}
	return stdout.String(), code
}
