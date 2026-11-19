package sandboxsession

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestWorkspaceRootPlanMaterialiseRunsPlanAndVerify pins the ci#1130 driver: the
// plan's idempotent materialisation commands run first, then the `test -d`
// verify argv, and only then is the root reported as existing.
func TestWorkspaceRootPlanMaterialiseRunsPlanAndVerify(t *testing.T) {
	plan, err := PlanWorkspaceRoot(WorkspaceIdentity{
		WorkspaceUUID: "ws-1",
		OSUser:        "coder-ws-1",
		Home:          "/home/coder-ws-1",
	}, "/workspace/coder-ws-1")
	if err != nil {
		t.Fatalf("PlanWorkspaceRoot: %v", err)
	}

	var ran [][]string
	observation, err := plan.Materialise(context.Background(), func(_ context.Context, argv []string) error {
		ran = append(ran, append([]string(nil), argv...))
		return nil
	})
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	if !observation.Exists || !observation.Materialised {
		t.Fatalf("observation must report an existing, materialised root, got %+v", observation)
	}
	if observation.Root != "/workspace/coder-ws-1" {
		t.Fatalf("observation root = %q", observation.Root)
	}

	want := [][]string{
		{"mkdir", "-p", "--", "/workspace/coder-ws-1"},
		{"chown", "-R", "coder-ws-1", "/workspace/coder-ws-1"},
		{"mkdir", "-p", "--", "/home/coder-ws-1"},
		{"chown", "-R", "coder-ws-1", "/home/coder-ws-1"},
		{"test", "-d", "/workspace/coder-ws-1"},
	}
	if len(ran) != len(want) {
		t.Fatalf("ran %d argv, want %d: %v", len(ran), len(want), ran)
	}
	for i := range want {
		if strings.Join(ran[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("argv[%d] = %v, want %v", i, ran[i], want[i])
		}
	}
}

// TestWorkspaceRootPlanMaterialiseFailsClosed pins that a failing materialisation
// or a failing verify never reports an existing root: the provision must fail
// with ErrWorkspaceRootMissing instead of handing a root-less workspace to a tool
// call (ci#1126 rule 1).
func TestWorkspaceRootPlanMaterialiseFailsClosed(t *testing.T) {
	plan, err := PlanWorkspaceRoot(WorkspaceIdentity{WorkspaceUUID: "ws-2", OSUser: "coder-ws-2", Home: "/home/coder-ws-2"}, "/workspace/coder-ws-2")
	if err != nil {
		t.Fatalf("PlanWorkspaceRoot: %v", err)
	}

	t.Run("materialise command fails", func(t *testing.T) {
		observation, err := plan.Materialise(context.Background(), func(_ context.Context, argv []string) error {
			if argv[0] == "chown" {
				return errors.New("chown: operation not permitted")
			}
			return nil
		})
		if !errors.Is(err, ErrWorkspaceRootMissing) {
			t.Fatalf("err = %v, want ErrWorkspaceRootMissing", err)
		}
		if observation.Exists {
			t.Fatalf("a failed materialisation must not report Exists=true: %+v", observation)
		}
	})

	t.Run("verify fails", func(t *testing.T) {
		observation, err := plan.Materialise(context.Background(), func(_ context.Context, argv []string) error {
			if argv[0] == "test" {
				return errors.New("exit status 1")
			}
			return nil
		})
		if !errors.Is(err, ErrWorkspaceRootMissing) {
			t.Fatalf("err = %v, want ErrWorkspaceRootMissing", err)
		}
		if observation.Exists {
			t.Fatalf("a failed verify must not report Exists=true: %+v", observation)
		}
	})

	t.Run("no runner", func(t *testing.T) {
		if _, err := plan.Materialise(context.Background(), nil); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
}

// TestWorkspaceRootProvisionerFuncSatisfiesContract pins that the adapter opts a
// provider into the ci#1126 capability, so EnsureWorkspaceRoot drives it.
func TestWorkspaceRootProvisionerFuncSatisfiesContract(t *testing.T) {
	provider := &materialiseStubProvider{name: "coder", provisioner: WorkspaceRootProvisionerFunc(
		func(_ context.Context, _ SandboxRef, spec WorkspaceRootSpec) (WorkspaceRootObservation, error) {
			plan, err := PlanWorkspaceRoot(WorkspaceIdentity{WorkspaceUUID: "ws-3", OSUser: "coder-ws-3", Home: "/home/coder-ws-3"}, spec.Root)
			if err != nil {
				return WorkspaceRootObservation{}, err
			}
			return plan.Materialise(context.Background(), func(context.Context, []string) error { return nil })
		})}

	if !SupportsWorkspaceRootProvisioning(provider) {
		t.Fatal("a provider with the adapter must satisfy the workspace root contract")
	}
	observation, err := EnsureWorkspaceRoot(context.Background(), provider, SandboxRef{Provider: "coder", SandboxUUID: "sb-1"},
		WorkspaceRootSpec{WorkspaceUUID: "ws-3", Root: "/workspace/coder-ws-3"})
	if err != nil {
		t.Fatalf("EnsureWorkspaceRoot: %v", err)
	}
	if !observation.Exists {
		t.Fatalf("observation = %+v", observation)
	}
	if line := observation.ReportLine(); !strings.Contains(line, "root=/workspace/coder-ws-3 exists=true") {
		t.Fatalf("report line = %q", line)
	}
}

// materialiseStubProvider is a minimal Provider used to exercise the capability plumbing.
type materialiseStubProvider struct {
	name        string
	provisioner WorkspaceRootProvisioner
}

func (s *materialiseStubProvider) Name() string { return s.name }

func (s *materialiseStubProvider) EnsureSandbox(context.Context, Request) (SandboxRef, error) {
	return SandboxRef{Provider: s.name, SandboxUUID: "sb-1"}, nil
}

func (s *materialiseStubProvider) InjectFiles(context.Context, SandboxRef, []FileInjection) error {
	return nil
}

func (s *materialiseStubProvider) OpenSession(context.Context, SandboxRef, ShellSpec) (Session, error) {
	return Session{Provider: s.name, SandboxUUID: "sb-1"}, nil
}

func (s *materialiseStubProvider) ProvisionWorkspaceRoot(ctx context.Context, ref SandboxRef, spec WorkspaceRootSpec) (WorkspaceRootObservation, error) {
	return s.provisioner.ProvisionWorkspaceRoot(ctx, ref, spec)
}
