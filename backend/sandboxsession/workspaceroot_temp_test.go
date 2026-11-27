package sandboxsession

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

const tempRootTestRoot = "/workspace/coder-11111111-2222-3333-4444-555555555555"

// A repo-less temp workspace always materialises the root and initialises an
// empty repository, and the git step is idempotent by construction.
func TestTempWorkspaceRootPlanRepoLessInitialisesRepo(t *testing.T) {
	plan, err := NewWorkspaceRootPlan(TempWorkspaceRootSpec("ws-temp", tempRootTestRoot, ""))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	want := [][]string{
		{"mkdir", "-p", "--", tempRootTestRoot},
		{"git", "-C", tempRootTestRoot, "init", "--quiet"},
	}
	if !reflect.DeepEqual(plan.Argv(), want) {
		t.Fatalf("argv = %#v, want %#v", plan.Argv(), want)
	}
	if want := [][]string{{"test", "-d", tempRootTestRoot}}; !reflect.DeepEqual(plan.VerifyArgv(), want) {
		t.Fatalf("verify argv = %#v, want %#v", plan.VerifyArgv(), want)
	}
}

// A non-temp root keeps the pre-existing plan: mkdir only, no git step.
func TestWorkspaceRootPlanNonTempHasNoGitStep(t *testing.T) {
	plan, err := NewWorkspaceRootPlan(WorkspaceRootSpec{WorkspaceUUID: "ws-pool", Root: tempRootTestRoot})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if want := [][]string{{"mkdir", "-p", "--", tempRootTestRoot}}; !reflect.DeepEqual(plan.Argv(), want) {
		t.Fatalf("argv = %#v, want %#v", plan.Argv(), want)
	}
}

// A repository-backed temp workspace clones only when no checkout is present, so
// double provisioning of the same root is safe.
func TestTempWorkspaceRootPlanRepoLeavesExistingRootAlone(t *testing.T) {
	const repo = "https://github.com/msgmate-io/open-chat-go.git"
	plan, err := NewWorkspaceRootPlan(TempWorkspaceRootSpec("ws-temp", tempRootTestRoot, repo))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	argv := plan.Argv()
	if len(argv) != 2 {
		t.Fatalf("argv = %#v, want root materialisation plus one clone step", argv)
	}
	clone := argv[len(argv)-1]
	if clone[0] != "sh" || clone[1] != "-c" {
		t.Fatalf("clone step = %#v, want a guarded sh -c", clone)
	}
	if clone[4] != tempRootTestRoot || clone[5] != repo {
		t.Fatalf("clone step args = %#v, want root %q and repo %q", clone, tempRootTestRoot, repo)
	}
}

// Materialising twice replays the same idempotent argv and stays healthy.
func TestEnsureTempWorkspaceRootIsIdempotent(t *testing.T) {
	ctx := context.Background()
	var runs [][]string
	run := func(_ context.Context, argv []string) error {
		runs = append(runs, append([]string(nil), argv...))
		return nil
	}

	for i := 0; i < 2; i++ {
		observation, err := EnsureTempWorkspaceRoot(ctx, TempWorkspaceRootSpec("ws-temp", tempRootTestRoot, ""), run)
		if err != nil {
			t.Fatalf("provision %d: %v", i, err)
		}
		if !observation.Exists || !observation.Materialised {
			t.Fatalf("provision %d: observation = %#v", i, observation)
		}
	}
	// mkdir + git init + verify, twice: the second pass replays exactly the same
	// argv, which is what makes double provisioning a no-op.
	if len(runs) != 6 {
		t.Fatalf("runner calls = %d, want 6", len(runs))
	}
	if !reflect.DeepEqual(runs[:3], runs[3:]) {
		t.Fatalf("second provision replayed %#v, want %#v", runs[3:], runs[:3])
	}
}

// A failing provisioner yields the typed provisioning error, keeps
// ErrWorkspaceRootMissing matchable and never reports the root as existing.
func TestEnsureTempWorkspaceRootFailsClosed(t *testing.T) {
	boom := errors.New("mkdir: permission denied")
	run := func(_ context.Context, _ []string) error { return boom }

	observation, err := EnsureTempWorkspaceRoot(context.Background(), TempWorkspaceRootSpec("ws-temp", tempRootTestRoot, ""), run)
	if err == nil {
		t.Fatal("expected a provisioning error")
	}
	if !errors.Is(err, ErrWorkspaceRootProvisioning) {
		t.Fatalf("error %v is not ErrWorkspaceRootProvisioning", err)
	}
	if !errors.Is(err, ErrWorkspaceRootMissing) {
		t.Fatalf("error %v is not ErrWorkspaceRootMissing", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error %v lost the runner cause", err)
	}
	if observation.Exists {
		t.Fatalf("observation = %#v, want Exists=false", observation)
	}
	if err := RequireMaterialisedWorkspaceRoot(tempRootTestRoot, observation); !errors.Is(err, ErrWorkspaceRootProvisioning) {
		t.Fatalf("turn-start guard = %v, want ErrWorkspaceRootProvisioning", err)
	}
}

// The turn-start guard accepts a materialised root and rejects an empty one.
func TestRequireMaterialisedWorkspaceRoot(t *testing.T) {
	if err := RequireMaterialisedWorkspaceRoot(tempRootTestRoot, WorkspaceRootObservation{Root: tempRootTestRoot, Exists: true}); err != nil {
		t.Fatalf("materialised root rejected: %v", err)
	}
	if err := RequireMaterialisedWorkspaceRoot("  ", WorkspaceRootObservation{Exists: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty root = %v, want ErrInvalidRequest", err)
	}
}

// Open refuses to start a turn for an auto_sandbox workspace whose temp root
// could not be materialised, and the error is the typed provisioning error.
func TestOpenAutoSandboxFailsClosedOnUnprovisionedTempRoot(t *testing.T) {
	provider, root := newRootedProvider("coder", false)
	root.err = errors.New("git: not found")
	reg := NewRegistry()
	if err := reg.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := Open(context.Background(), reg, Request{
		Provider:    "coder",
		OwnerUUID:   "owner-1",
		AutoSandbox: true,
		Shell:       ShellSpec{WorkDir: tempRootTestRoot},
	})
	if err == nil {
		t.Fatal("expected Open to fail for an unprovisioned temp root")
	}
	if !errors.Is(err, ErrWorkspaceRootProvisioning) {
		t.Fatalf("Open error %v is not ErrWorkspaceRootProvisioning", err)
	}
	if provider.sessionCalls != 0 {
		t.Fatalf("session opened %d times, want 0 (turn must not start)", provider.sessionCalls)
	}
}

// An auto_sandbox request reaches the provisioner with the temp spec, so the
// provider materialises the root and the git step unconditionally.
func TestOpenAutoSandboxPlansTempRoot(t *testing.T) {
	provider, root := newRootedProvider("coder", true)
	reg := NewRegistry()
	if err := reg.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := Open(context.Background(), reg, Request{
		Provider:    "coder",
		OwnerUUID:   "owner-1",
		AutoSandbox: true,
		Repo:        "https://github.com/msgmate-io/open-chat-go.git",
		Shell:       ShellSpec{WorkDir: tempRootTestRoot},
	}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !root.gotSpec.Temp {
		t.Fatalf("provisioner spec = %#v, want Temp=true", root.gotSpec)
	}
	if root.gotSpec.Repo != "https://github.com/msgmate-io/open-chat-go.git" {
		t.Fatalf("provisioner spec repo = %q", root.gotSpec.Repo)
	}
}
