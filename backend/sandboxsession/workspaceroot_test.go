package sandboxsession

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// rootProvider is a fakeProvider that satisfies the ci#1126 workspace root
// materialisation contract the way the private msgmate_coder hook must: it runs
// the plan argv inside the sandbox, then reports what it observed. rootExists
// drives the observation, so a misprovisioned (root-less) sandbox - exactly the
// live canary failure - is reproducible offline.
type rootProvider struct {
	fakeProvider

	rootExists bool
	err        error

	calls     int
	gotRef    SandboxRef
	gotSpec   WorkspaceRootSpec
	ranArgv   [][]string
	ranVerify [][]string
}

func (f *rootProvider) ProvisionWorkspaceRoot(_ context.Context, ref SandboxRef, spec WorkspaceRootSpec) (WorkspaceRootObservation, error) {
	f.calls++
	f.gotRef = ref
	f.gotSpec = spec
	if f.err != nil {
		return WorkspaceRootObservation{}, f.err
	}

	plan, err := NewWorkspaceRootPlan(spec)
	if err != nil {
		return WorkspaceRootObservation{}, err
	}
	// Stand in for the hook executing plan.Argv() / plan.VerifyArgv() in the
	// container: record them so the test can pin the idempotent contract.
	f.ranArgv = plan.Argv()
	f.ranVerify = plan.VerifyArgv()
	return WorkspaceRootObservation{Root: plan.Root, Exists: f.rootExists, Materialised: f.rootExists}, nil
}

const (
	rootTestUUID = "0b6f3f6e-1f0a-4b3f-9d33-3c8f0a1b2c3d"
	rootTestRoot = "/workspace/coder-0b6f3f6e-1f0a-4b3f-9d33-3c8f0a1b2c3d"
	rootTestHome = "/home/ws-0b6f3f6e"
)

func rootTestSpec() WorkspaceRootSpec {
	return WorkspaceRootSpec{
		WorkspaceUUID: rootTestUUID,
		Root:          rootTestRoot,
		OSUser:        "ws-0b6f3f6e",
		Home:          rootTestHome,
	}
}

func rootTestRef() SandboxRef {
	return SandboxRef{Provider: "msgmate_coder_docker", SandboxUUID: rootTestUUID}
}

// TestEnsureWorkspaceRootFailsClosedWhenRootIsMissing is the ci#1126 regression
// test: a provision whose workspace root was never materialised must FAIL the
// provision with a provisioning error, so the session is never handed to a tool
// call - which is what produced
// `coder tool api failed (400): ... can't cd to /workspace/coder-<uuid>`.
func TestEnsureWorkspaceRootFailsClosedWhenRootIsMissing(t *testing.T) {
	provider := &rootProvider{fakeProvider: fakeProvider{name: "msgmate_coder_docker"}}

	observation, err := EnsureWorkspaceRoot(context.Background(), provider, rootTestRef(), rootTestSpec())
	if err == nil {
		t.Fatal("EnsureWorkspaceRoot() error = nil, want a provisioning failure (a root-less workspace must fail closed)")
	}
	if !errors.Is(err, ErrWorkspaceRootMissing) {
		t.Fatalf("EnsureWorkspaceRoot() error = %v, want ErrWorkspaceRootMissing", err)
	}
	if observation.Exists {
		t.Fatal("observation.Exists = true, want false")
	}
	if provider.calls != 1 {
		t.Fatalf("provision calls = %d, want 1", provider.calls)
	}
	if provider.gotRef.SandboxUUID != rootTestUUID {
		t.Fatalf("provision ref = %+v, want the provisioned sandbox", provider.gotRef)
	}
	// The error must name the provider and the missing root so the CI report
	// points straight at the sandbox.
	if !strings.Contains(err.Error(), "msgmate_coder_docker") {
		t.Fatalf("error %q does not name the provider", err)
	}
	if !strings.Contains(err.Error(), rootTestRoot) {
		t.Fatalf("error %q does not name the missing root %q", err, rootTestRoot)
	}
}

// TestEnsureWorkspaceRootMaterialisesHealthyProvision pins the other half: a
// healthy provision still succeeds, and the plan handed to the provider is the
// idempotent `mkdir -p` + chown contract, so replaying it on adopt or after a
// pool restart is a no-op for an existing root.
func TestEnsureWorkspaceRootMaterialisesHealthyProvision(t *testing.T) {
	provider := &rootProvider{fakeProvider: fakeProvider{name: "msgmate_coder_docker"}, rootExists: true}

	observation, err := EnsureWorkspaceRoot(context.Background(), provider, rootTestRef(), rootTestSpec())
	if err != nil {
		t.Fatalf("EnsureWorkspaceRoot() error = %v, want nil for a materialised root", err)
	}
	if !observation.Exists || observation.Root != rootTestRoot || !observation.Materialised {
		t.Fatalf("observation = %+v, want root %q exists=true materialised=true", observation, rootTestRoot)
	}

	wantArgv := [][]string{
		{"mkdir", "-p", "--", rootTestRoot},
		{"chown", "-R", "ws-0b6f3f6e", rootTestRoot},
		{"mkdir", "-p", "--", rootTestHome},
		{"chown", "-R", "ws-0b6f3f6e", rootTestHome},
	}
	if !reflect.DeepEqual(provider.ranArgv, wantArgv) {
		t.Fatalf("materialisation argv = %#v, want %#v", provider.ranArgv, wantArgv)
	}
	if want := [][]string{{"test", "-d", rootTestRoot}}; !reflect.DeepEqual(provider.ranVerify, want) {
		t.Fatalf("verify argv = %#v, want %#v", provider.ranVerify, want)
	}

	// Replay must be idempotent: running the contract again on a healthy
	// workspace plans exactly the same commands and still succeeds.
	if _, err := EnsureWorkspaceRoot(context.Background(), provider, rootTestRef(), rootTestSpec()); err != nil {
		t.Fatalf("second EnsureWorkspaceRoot() error = %v, want nil (idempotent adoption)", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provision calls = %d, want 2", provider.calls)
	}
	if !reflect.DeepEqual(provider.ranArgv, wantArgv) {
		t.Fatalf("replayed argv = %#v, want the same idempotent argv", provider.ranArgv)
	}

	line := observation.ReportLine()
	if !strings.Contains(line, "exists=true") || strings.Contains(line, "\n") {
		t.Fatalf("ReportLine() = %q, want a single line reporting exists=true", line)
	}
	if strings.Contains(line, "\t") {
		t.Fatalf("ReportLine() = %q, want a plain key=value line", line)
	}
}

// TestEnsureWorkspaceRootPropagatesProviderFailure keeps the fail-closed shape
// when the provider itself errors (eg mkdir/chown failed in the sandbox).
func TestEnsureWorkspaceRootPropagatesProviderFailure(t *testing.T) {
	wantErr := errors.New("mkdir: permission denied")
	provider := &rootProvider{fakeProvider: fakeProvider{name: "msgmate_coder_docker"}, err: wantErr}

	if _, err := EnsureWorkspaceRoot(context.Background(), provider, rootTestRef(), rootTestSpec()); !errors.Is(err, wantErr) {
		t.Fatalf("EnsureWorkspaceRoot() error = %v, want it to wrap %v", err, wantErr)
	}
}

// TestWorkspaceRootPlanAssert proves the post-provision assertion fails closed
// when the provider reports a different or non-absolute root than planned.
func TestWorkspaceRootPlanAssert(t *testing.T) {
	plan, err := NewWorkspaceRootPlan(rootTestSpec())
	if err != nil {
		t.Fatalf("NewWorkspaceRootPlan() error = %v", err)
	}
	if err := plan.Assert(WorkspaceRootObservation{Root: "/workspace/coder-someone-else", Exists: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Assert(other root) error = %v, want ErrInvalidRequest", err)
	}
	if err := plan.Assert(WorkspaceRootObservation{Root: "relative/root", Exists: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Assert(relative root) error = %v, want ErrInvalidRequest", err)
	}
	if err := plan.Assert(WorkspaceRootObservation{Root: rootTestRoot, Exists: false}); !errors.Is(err, ErrWorkspaceRootMissing) {
		t.Fatalf("Assert(missing) error = %v, want ErrWorkspaceRootMissing", err)
	}
	if err := plan.Assert(WorkspaceRootObservation{Root: rootTestRoot, Exists: true}); err != nil {
		t.Fatalf("Assert(healthy) error = %v, want nil", err)
	}
}

// TestEnsureWorkspaceRootRequiresTheContract keeps core honest: a provider that
// cannot materialise a root is reported as unsupported instead of being assumed
// healthy, so the integration cannot silently skip the check.
func TestEnsureWorkspaceRootRequiresTheContract(t *testing.T) {
	provider := &plainProvider{fakeProvider: fakeProvider{name: "plain"}}

	if _, err := EnsureWorkspaceRoot(context.Background(), provider, rootTestRef(), rootTestSpec()); !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("EnsureWorkspaceRoot(plainProvider) error = %v, want ErrUnsupportedOperation", err)
	}
	if SupportsWorkspaceRootProvisioning(provider) {
		t.Fatal("SupportsWorkspaceRootProvisioning(plainProvider) = true, want false")
	}

	rooted := &rootProvider{fakeProvider: fakeProvider{name: "rooted"}, rootExists: true}
	if !SupportsWorkspaceRootProvisioning(rooted) {
		t.Fatal("SupportsWorkspaceRootProvisioning(rootProvider) = false, want true")
	}
	if _, err := EnsureWorkspaceRoot(context.Background(), nil, rootTestRef(), rootTestSpec()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("EnsureWorkspaceRoot(nil) error = %v, want ErrInvalidRequest", err)
	}
	// An unresolvable root is rejected before the provider is even asked.
	if _, err := EnsureWorkspaceRoot(context.Background(), rooted, rootTestRef(), WorkspaceRootSpec{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("EnsureWorkspaceRoot(empty root) error = %v, want ErrInvalidRequest", err)
	}
}

// TestPlanWorkspaceRootUsesIdentity pins the ci#603 identity wiring: the plan the
// provider executes chowns the root to the per-workspace OS user and materialises
// the per-agent HOME, so a replayed/adopted workspace is usable by the agent.
func TestPlanWorkspaceRootUsesIdentity(t *testing.T) {
	identity := WorkspaceIdentity{WorkspaceUUID: rootTestUUID, OSUser: "ws-0b6f3f6e", Home: rootTestHome}

	plan, err := PlanWorkspaceRoot(identity, rootTestRoot)
	if err != nil {
		t.Fatalf("PlanWorkspaceRoot() error = %v", err)
	}
	if plan.Root != rootTestRoot || plan.OSUser != "ws-0b6f3f6e" || plan.Home != rootTestHome {
		t.Fatalf("plan = %+v, want root/os_user/home from the identity", plan)
	}
	want := [][]string{
		{"mkdir", "-p", "--", rootTestRoot},
		{"chown", "-R", "ws-0b6f3f6e", rootTestRoot},
		{"mkdir", "-p", "--", rootTestHome},
		{"chown", "-R", "ws-0b6f3f6e", rootTestHome},
	}
	if !reflect.DeepEqual(plan.Argv(), want) {
		t.Fatalf("plan argv = %#v, want %#v", plan.Argv(), want)
	}

	// A half-provisioned identity (no HOME yet) cannot yield a plan at all: the
	// replay/adoption path fails closed instead of materialising a root for an
	// identity that has not been provisioned.
	if _, err := PlanWorkspaceRoot(WorkspaceIdentity{WorkspaceUUID: rootTestUUID, OSUser: "ws-0b6f3f6e"}, rootTestRoot); err == nil {
		t.Fatal("PlanWorkspaceRoot(unprovisioned identity) error = nil, want it to reject an identity without a HOME")
	}
}

// TestNewWorkspaceRootPlanRejectsUnusableRoots pins the plan's input validation:
// only an absolute, clean, non-root path is materialisable, so a provider is
// never handed a plan that would mkdir/chown something dangerous.
func TestNewWorkspaceRootPlanRejectsUnusableRoots(t *testing.T) {
	for _, root := range []string{"", "   ", "workspace/coder-x", "/", "/workspace/../etc", "/workspace/coder-x/"} {
		spec := rootTestSpec()
		spec.Root = root
		if _, err := NewWorkspaceRootPlan(spec); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("NewWorkspaceRootPlan(root=%q) error = %v, want ErrInvalidRequest", root, err)
		}
	}

	spec := rootTestSpec()
	spec.Home = "home/ws-0b6f3f6e"
	if _, err := NewWorkspaceRootPlan(spec); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("NewWorkspaceRootPlan(relative home) error = %v, want ErrInvalidRequest", err)
	}
}
