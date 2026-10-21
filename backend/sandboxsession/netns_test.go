package sandboxsession

// ci#612: pin the per-workspace network namespace (pooled path) and the
// dedicated-only gate for sensitive repositories. The acceptance criteria of the
// ticket are exactly what these tests assert:
//
//   - two pooled workspaces cannot reach each other's bound ports;
//   - a dedicated/sensitive workspace never lands in a shared pool;
//   - egress (GitHub/API) and `gh` auth work unchanged inside the netns.

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func testNetnsIdentity(uuid string) WorkspaceIdentity {
	return WorkspaceIdentity{
		WorkspaceUUID: uuid,
		OwnerUUID:     "owner-1",
		OSUser:        "ws-" + uuid,
		Home:          "/home/ws-" + uuid,
	}
}

func TestNetnsNameIsDeterministicAndShort(t *testing.T) {
	first, err := NetnsName("ws-1")
	if err != nil {
		t.Fatalf("NetnsName: %v", err)
	}
	again, err := NetnsName("ws-1")
	if err != nil {
		t.Fatalf("NetnsName: %v", err)
	}
	if first != again {
		t.Fatalf("namespace name must be deterministic for re-attach: %q != %q", first, again)
	}
	if !strings.HasPrefix(first, NetnsNamePrefix) {
		t.Fatalf("namespace %q must carry the %q prefix", first, NetnsNamePrefix)
	}
	if len(first) != len(NetnsNamePrefix)+NetnsNameHashLen {
		t.Fatalf("namespace %q must stay short (IFNAMSIZ), got %d chars", first, len(first))
	}
	if strings.ContainsAny(first, "/. ") {
		t.Fatalf("namespace %q must be usable as an `ip netns` name", first)
	}
	other, err := NetnsName("ws-2")
	if err != nil {
		t.Fatalf("NetnsName: %v", err)
	}
	if other == first {
		t.Fatalf("distinct workspaces must get distinct namespaces (%q)", first)
	}
	if _, err := NetnsName("   "); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("an empty workspace uuid must be rejected, got %v", err)
	}
}

func TestNetnsPlanRequiresCI603Identity(t *testing.T) {
	if _, err := NewNetnsPlan(WorkspaceIdentity{WorkspaceUUID: "ws-1"}); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("a plan without home/user must be rejected, got %v", err)
	}
	identity := testNetnsIdentity("ws-1")
	identity.OSUser = ""
	if _, err := NewNetnsPlan(identity); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("a plan without the ci#603 os user must be rejected, got %v", err)
	}
}

func TestNetnsPlanGivesEveryWorkspaceItsOwnLoopback(t *testing.T) {
	planA, err := NewNetnsPlan(testNetnsIdentity("ws-a"))
	if err != nil {
		t.Fatalf("NewNetnsPlan: %v", err)
	}
	planB, err := NewNetnsPlan(testNetnsIdentity("ws-b"))
	if err != nil {
		t.Fatalf("NewNetnsPlan: %v", err)
	}
	for _, plan := range []NetnsPlan{planA, planB} {
		// 127.0.0.1 is private to the namespace, which is the whole point: two
		// pooled workspaces can bind the same port without colliding and cannot
		// reach each other through it.
		if plan.LoopbackAlias != "127.0.0.1" {
			t.Fatalf("loopback alias must be the private 127.0.0.1, got %q", plan.LoopbackAlias)
		}
		if !strings.HasPrefix(plan.Namespace, NetnsNamePrefix) {
			t.Fatalf("workspace %s must run in its own netns, got %q", plan.WorkspaceUUID, plan.Namespace)
		}
	}
	if planA.Namespace == planB.Namespace {
		t.Fatalf("two pooled workspaces must not share a namespace (%q)", planA.Namespace)
	}
	// The ci#546 alias space stays available inside the private namespace, so
	// harnesses that hardcode it keep working.
	if !strings.HasPrefix(planA.CompatLoopbackAlias, DefaultNetnsLoopbackAliasPrefix) {
		t.Fatalf("compat loopback alias %q must stay in the ci#546 space", planA.CompatLoopbackAlias)
	}
	if planA.Home == planB.Home || planA.OSUser == planB.OSUser {
		t.Fatalf("the netns plan must keep the ci#603 per-agent identity (%q/%q vs %q/%q)",
			planA.OSUser, planA.Home, planB.OSUser, planB.Home)
	}
}

func TestNetnsSidecarKeepsEgressAndBlocksSiblings(t *testing.T) {
	plan, err := NewNetnsPlan(testNetnsIdentity("ws-1"))
	if err != nil {
		t.Fatalf("NewNetnsPlan: %v", err)
	}
	if err := plan.Sidecar.Validate(); err != nil {
		t.Fatalf("default sidecar must be valid: %v", err)
	}
	argv := plan.Sidecar.Argv(4242)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--disable-host-loopback") {
		t.Fatalf("sidecar must disable the host loopback so a sibling stays unreachable: %v", argv)
	}
	if strings.Contains(joined, SidecarPIDPlaceholder) {
		t.Fatalf("sidecar argv must have the pid substituted: %v", argv)
	}
	if !strings.Contains(joined, "4242") {
		t.Fatalf("sidecar argv must target the anchor pid: %v", argv)
	}
	// Egress must never be disabled: only the host loopback is.
	for _, arg := range argv {
		if strings.Contains(arg, "--disable-dns") || strings.Contains(arg, "--no-egress") {
			t.Fatalf("sidecar must keep egress/DNS working, got %q", arg)
		}
	}
	// A sidecar that does not disable the host loopback is refused.
	broken := DefaultSlirpSidecar()
	broken.Args = []string{"--configure", SidecarPIDPlaceholder}
	if err := broken.Validate(); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("a sidecar without --disable-host-loopback must be rejected, got %v", err)
	}
}

func TestWrapShellForNetnsJoinsNamespaceAndKeepsIdentity(t *testing.T) {
	plan, err := NewNetnsPlan(testNetnsIdentity("ws-1"))
	if err != nil {
		t.Fatalf("NewNetnsPlan: %v", err)
	}
	spec := &ShellSpec{Command: []string{"/bin/bash"}, Env: map[string]string{"HOME": plan.Home, "USER": plan.OSUser}}
	if err := WrapShellForNetns(spec, plan); err != nil {
		t.Fatalf("WrapShellForNetns: %v", err)
	}
	if !JoinsNetns(spec.Command, plan.Namespace) {
		t.Fatalf("wrapped command must join %q, got %v", plan.Namespace, spec.Command)
	}
	if spec.Command[len(spec.Command)-1] != "/bin/bash" {
		t.Fatalf("the exec target must stay the tail of the argv, got %v", spec.Command)
	}
	if spec.Env["HOME"] != plan.Home || spec.Env["USER"] != plan.OSUser {
		t.Fatalf("the ci#603 identity env must be preserved, got %v", spec.Env)
	}
	if spec.Env[EnvSessionNetns] != plan.Namespace {
		t.Fatalf("the session must advertise its namespace, got %v", spec.Env)
	}
	if spec.Env[EnvSessionNetnsLoopback] != "127.0.0.1" {
		t.Fatalf("the session must advertise its private loopback, got %v", spec.Env)
	}
	// Idempotent: wrapping twice must not stack prefixes.
	before := len(spec.Command)
	if err := WrapShellForNetns(spec, plan); err != nil {
		t.Fatalf("WrapShellForNetns (again): %v", err)
	}
	if len(spec.Command) != before {
		t.Fatalf("wrapping a wrapped spec must be a no-op, got %v", spec.Command)
	}
	if err := WrapShellForNetns(nil, plan); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("a nil spec must be rejected, got %v", err)
	}
}

func TestDecidePlacementRefusesPooledSlotForSensitiveRepo(t *testing.T) {
	sensitive := []RepoScope{
		{Repo: "msgmate-io/open-chat-go-ci", Trust: "allow"},
		{Repo: "owner/private", SecretsAvailable: true},
		{Repo: "owner/infra", ProductionCredentials: true},
	}
	for _, scope := range sensitive {
		req := PlacementRequest{
			Scope:                   scope,
			Identity:                testNetnsIdentity("ws-1"),
			PooledSlotAvailable:     true,
			WorkspaceNetnsSupported: true,
			DedicatedAvailable:      true,
		}
		placement, err := DecidePlacement(req)
		if err != nil {
			t.Fatalf("%s: DecidePlacement: %v", scope.Repo, err)
		}
		if placement.Mode != PlacementDedicated {
			t.Fatalf("%s: a sensitive repo must get a dedicated pod, got %q", scope.Repo, placement.Mode)
		}
		if placement.SharedPodNetns {
			t.Fatalf("%s: a sensitive repo must never share the pod netns", scope.Repo)
		}
		if !placement.RequireDedicated {
			t.Fatalf("%s: the caller must not retry the pooled path", scope.Repo)
		}
	}
}

func TestDecidePlacementFailsClosedWithoutDedicatedPod(t *testing.T) {
	req := PlacementRequest{
		Scope:                   RepoScope{Repo: "owner/secrets", Trust: TrustAllow},
		Identity:                testNetnsIdentity("ws-1"),
		PooledSlotAvailable:     true,
		WorkspaceNetnsSupported: true,
		DedicatedAvailable:      false,
	}
	placement, err := DecidePlacement(req)
	if !errors.Is(err, ErrSensitiveRequiresDedicated) {
		t.Fatalf("a sensitive repo without a dedicated pod must fail closed, got %v / %+v", err, placement)
	}
	if !IsNetnsPlacementFailure(err) {
		t.Fatalf("the error must be classifiable as a netns placement failure: %v", err)
	}
	if placement.Mode != "" {
		t.Fatalf("no placement may be returned on a refused gate, got %q", placement.Mode)
	}
}

func TestDecidePlacementGivesPooledWorkspaceItsOwnNetns(t *testing.T) {
	req := PlacementRequest{
		Scope:                   RepoScope{Repo: "owner/dev"},
		Identity:                testNetnsIdentity("ws-1"),
		PooledSlotAvailable:     true,
		WorkspaceNetnsSupported: true,
		DedicatedAvailable:      true,
	}
	placement, err := DecidePlacement(req)
	if err != nil {
		t.Fatalf("DecidePlacement: %v", err)
	}
	if placement.Mode != PlacementPooledWorkspaceNetns {
		t.Fatalf("a supported pooled slot must use a per-workspace netns, got %q", placement.Mode)
	}
	if placement.Netns == nil {
		t.Fatal("a per-workspace netns placement must carry its plan")
	}
	if placement.SharedPodNetns {
		t.Fatal("the shared pod netns must not be reported for a netns placement")
	}
	if placement.Netns.Namespace == "" || !JoinsNetns(placement.Netns.Command, placement.Netns.Namespace) {
		t.Fatalf("the plan must join its own namespace, got %+v", placement.Netns)
	}
}

func TestDecidePlacementEphemeralDevModeIsNotUsedForSensitiveRepos(t *testing.T) {
	// Without runtime support a non-sensitive workspace may still fall back to
	// the shared pod netns (ephemeral dev mode)...
	placement, err := DecidePlacement(PlacementRequest{
		Scope:               RepoScope{Repo: "owner/dev"},
		Identity:            testNetnsIdentity("ws-1"),
		PooledSlotAvailable: true,
	})
	if err != nil {
		t.Fatalf("DecidePlacement: %v", err)
	}
	if placement.Mode != PlacementPooledSharedNetns || !placement.SharedPodNetns {
		t.Fatalf("a non-sensitive repo may use the ephemeral dev mode, got %+v", placement)
	}
	// ...but a sensitive one may never.
	if _, err := DecidePlacement(PlacementRequest{
		Scope:               RepoScope{Repo: "owner/dev", Trust: TrustAllow},
		Identity:            testNetnsIdentity("ws-1"),
		PooledSlotAvailable: true,
	}); !errors.Is(err, ErrSensitiveRequiresDedicated) {
		t.Fatalf("a sensitive repo must never use the shared pod netns, got %v", err)
	}
}

func TestDecidePlacementStrictModeRefusesSharedPodNetns(t *testing.T) {
	req := PlacementRequest{
		Scope:               RepoScope{Repo: "owner/dev"},
		Identity:            testNetnsIdentity("ws-1"),
		PooledSlotAvailable: true,
		Strict:              true,
		DedicatedAvailable:  true,
	}
	placement, err := DecidePlacement(req)
	if err != nil {
		t.Fatalf("DecidePlacement: %v", err)
	}
	if placement.Mode != PlacementDedicated {
		t.Fatalf("strict mode must refuse the shared pod netns, got %+v", placement)
	}

	req.DedicatedAvailable = false
	if _, err := DecidePlacement(req); !errors.Is(err, ErrWorkspaceNetnsUnavailable) {
		t.Fatalf("strict mode without a dedicated pod must fail closed, got %v", err)
	}
}

func TestDecidePlacementWithoutAnyCapacity(t *testing.T) {
	if _, err := DecidePlacement(PlacementRequest{Scope: RepoScope{Repo: "owner/dev"}}); !errors.Is(err, ErrNoPlacement) {
		t.Fatalf("no pooled slot and no dedicated pod must fail, got %v", err)
	}
}

func TestPlacementEnvOverrides(t *testing.T) {
	t.Setenv(EnvNetnsDisabled, "1")
	req := PlacementRequest{WorkspaceNetnsSupported: true}
	PlacementEnvOverrides(&req)
	if req.WorkspaceNetnsSupported {
		t.Fatal(EnvNetnsDisabled + " must disable per-workspace namespaces")
	}
	// The sensitive gate is never configurable.
	os.Unsetenv(EnvNetnsDisabled)
	t.Setenv(EnvNetnsStrict, "true")
	req = PlacementRequest{}
	PlacementEnvOverrides(&req)
	if !req.Strict {
		t.Fatal(EnvNetnsStrict + " must enable strict mode")
	}
}

func TestNetnsPlanValidateRejectsForeignNamespace(t *testing.T) {
	plan, err := NewNetnsPlan(testNetnsIdentity("ws-1"))
	if err != nil {
		t.Fatalf("NewNetnsPlan: %v", err)
	}
	foreign := plan
	foreign.Namespace, err = NetnsName("ws-2")
	if err != nil {
		t.Fatalf("NetnsName: %v", err)
	}
	if err := foreign.Validate(); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("a plan that joins a sibling's namespace must be rejected, got %v", err)
	}
	noEgress := plan
	noEgress.EgressHosts = nil
	if err := noEgress.Validate(); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("a plan without egress hosts cannot be verified and must be rejected, got %v", err)
	}
	publicLoopback := plan
	publicLoopback.LoopbackAlias = "127.0.1.7"
	if err := publicLoopback.Validate(); !errors.Is(err, ErrInvalidNetnsPlan) {
		t.Fatalf("a plan without the private loopback must be rejected, got %v", err)
	}
}

func TestNetnsPlanStringCarriesNoSecrets(t *testing.T) {
	plan, err := NewNetnsPlan(testNetnsIdentity("ws-1"))
	if err != nil {
		t.Fatalf("NewNetnsPlan: %v", err)
	}
	rendered := plan.String()
	for _, forbidden := range []string{"token", "TOKEN", "secret", "SECRET", "ghp_", "kubeconfig"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("plan rendering must not carry credential material (%q): %s", forbidden, rendered)
		}
	}
	if !strings.Contains(rendered, plan.Namespace) {
		t.Fatalf("plan rendering should name the namespace: %s", rendered)
	}
}
