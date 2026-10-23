package sandboxsession

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testIdentity(uuid, home string) WorkspaceIdentity {
	return WorkspaceIdentity{
		WorkspaceUUID: uuid,
		OwnerUUID:     "owner-1",
		OSUser:        "ws-" + uuid,
		Home:          home,
	}
}

func testGithubCred() CredentialRef {
	return CredentialRef{
		Name:        "github",
		EnvVars:     []string{"GH_TOKEN", "GITHUB_TOKEN"},
		Source:      "integration:github/main-bot",
		HomeRelPath: ".config/gh/hosts.yml",
	}
}

func testBinding(uuid, home string) PoolBinding {
	return PoolBinding{Identity: testIdentity(uuid, home), Credentials: []CredentialRef{testGithubCred()}}
}

// TestPoolRestartReplaysEveryLiveWorkspace is the acceptance test for ci#614:
// after a pool pod restart, provisioning is replayed for every live workspace
// bound to the pool, into its own per-agent HOME, without minting a new lease.
func TestPoolRestartReplaysEveryLiveWorkspace(t *testing.T) {
	registry := NewPoolRegistry()
	boundAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, ws := range []struct{ uuid, home string }{
		{"ws-1", "/home/ws-ws-1"},
		{"ws-2", "/home/ws-ws-2"},
	} {
		binding := testBinding(ws.uuid, ws.home)
		binding.BoundAt = boundAt
		if err := registry.Bind("docker_sandbox", "pool-1", binding); err != nil {
			t.Fatalf("bind %s: %v", ws.uuid, err)
		}
	}
	// A released workspace must never be replayed.
	released := testBinding("ws-3", "/home/ws-ws-3")
	released.Released = true
	if err := registry.Bind("docker_sandbox", "pool-1", released); err != nil {
		t.Fatalf("bind released: %v", err)
	}
	// A binding on another pool must not leak into this replay.
	if err := registry.Bind("docker_sandbox", "pool-2", testBinding("ws-9", "/home/ws-ws-9")); err != nil {
		t.Fatalf("bind other pool: %v", err)
	}

	plan := registry.PlanReplay(PoolRestart{
		Provider:    "docker_sandbox",
		SandboxUUID: "pool-1",
		ContainerID: "restarted-container",
		OccurredAt:  time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC),
	}, DefaultReplayPolicy())

	if len(plan.Targets) != 2 {
		t.Fatalf("expected 2 replay targets, got %d (deferred=%d)", len(plan.Targets), len(plan.Deferred))
	}
	if plan.SkippedReleased != 1 {
		t.Fatalf("expected 1 released binding skipped, got %d", plan.SkippedReleased)
	}
	if plan.Empty() {
		t.Fatal("expected a non-empty plan")
	}
	for _, target := range plan.Targets {
		if target.BoundAt != boundAt {
			t.Fatalf("replay must reuse the binding, not mint a lease: BoundAt=%s", target.BoundAt)
		}
		want := target.Identity.Home + "/.config/gh/hosts.yml"
		if len(target.HomePaths) != 1 || target.HomePaths[0] != want {
			t.Fatalf("workspace %s: per-agent home paths = %v, want [%s]", target.Identity.WorkspaceUUID, target.HomePaths, want)
		}
		if strings.Join(target.EnvVars, ",") != "GH_TOKEN,GITHUB_TOKEN" {
			t.Fatalf("workspace %s: session env = %v", target.Identity.WorkspaceUUID, target.EnvVars)
		}
	}

	// Re-observing the same container generation with a fresh credential is a
	// no-op; a restart into a fresh container always replays.
	now := plan.OccurredAt
	for _, target := range plan.Targets {
		if !registry.RecordReplay("docker_sandbox", "pool-1", target.Identity.WorkspaceUUID, "restarted-container", now, DefaultReplayPolicy()) {
			t.Fatalf("record replay for %s failed", target.Identity.WorkspaceUUID)
		}
	}
	sameGeneration := registry.PlanReplay(PoolRestart{
		Provider: "docker_sandbox", SandboxUUID: "pool-1",
		ContainerID: "restarted-container", OccurredAt: now.Add(time.Second),
	}, DefaultReplayPolicy())
	if len(sameGeneration.Targets) != 0 {
		t.Fatalf("expected no replay for the same container generation, got %d", len(sameGeneration.Targets))
	}
	for _, deferred := range sameGeneration.Deferred {
		if deferred.Reason != "already_reprovisioned_for_container" {
			t.Fatalf("unexpected defer reason %q", deferred.Reason)
		}
	}

	freshContainer := registry.PlanReplay(PoolRestart{
		Provider: "docker_sandbox", SandboxUUID: "pool-1",
		ContainerID: "brand-new-container", OccurredAt: now.Add(2 * time.Second),
	}, DefaultReplayPolicy())
	if len(freshContainer.Targets) != 2 {
		t.Fatalf("a restart into a fresh container must replay every workspace, got %d", len(freshContainer.Targets))
	}
}

// TestReplayBoundStopsRestartStampede pins the second half of the acceptance:
// a mass pool restart is reported, not looped.
func TestReplayBoundStopsRestartStampede(t *testing.T) {
	registry := NewPoolRegistry()
	policy := DefaultReplayPolicy()
	policy.MaxPerWindow = 2
	policy.RefreshAhead = 0
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	binding := testBinding("ws-1", "/home/ws-ws-1")
	binding.BoundAt = base
	if err := registry.Bind("docker_sandbox", "pool-1", binding); err != nil {
		t.Fatalf("bind: %v", err)
	}

	for i := 0; i < 2; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		plan := registry.PlanReplay(PoolRestart{
			Provider: "docker_sandbox", SandboxUUID: "pool-1",
			ContainerID: "container-" + string(rune('a'+i)), OccurredAt: at,
		}, policy)
		if len(plan.Targets) != 1 {
			t.Fatalf("replay %d: expected 1 target, got %d", i, len(plan.Targets))
		}
		registry.RecordReplay("docker_sandbox", "pool-1", "ws-1", "container-"+string(rune('a'+i)), at, policy)
	}

	plan := registry.PlanReplay(PoolRestart{
		Provider: "docker_sandbox", SandboxUUID: "pool-1",
		ContainerID: "container-c", OccurredAt: base.Add(3 * time.Second),
	}, policy)
	if len(plan.Targets) != 0 {
		t.Fatalf("expected the replay bound to stop the stampede, got %d targets", len(plan.Targets))
	}
	if len(plan.Deferred) != 1 || plan.Deferred[0].Reason != "replay_bound_reached" {
		t.Fatalf("expected a replay_bound_reached deferral, got %+v", plan.Deferred)
	}
	if !strings.Contains(strings.Join(plan.ReportLines(), "\n"), "deferred=") {
		t.Fatal("expected the deferral to be reportable")
	}

	// Once the window rolls over the identity is served again.
	rolled := registry.PlanReplay(PoolRestart{
		Provider: "docker_sandbox", SandboxUUID: "pool-1",
		ContainerID: "container-d", OccurredAt: base.Add(DefaultReplayPolicy().Window + time.Second),
	}, policy)
	if len(rolled.Targets) != 1 {
		t.Fatalf("expected the window rollover to re-enable replay, got %d", len(rolled.Targets))
	}
}

// TestReprovisionOnRestartCanBeDisabled keeps the deployment escape hatch.
func TestReprovisionOnRestartCanBeDisabled(t *testing.T) {
	registry := NewPoolRegistry()
	if err := registry.Bind("docker_sandbox", "pool-1", testBinding("ws-1", "/home/ws-ws-1")); err != nil {
		t.Fatalf("bind: %v", err)
	}
	policy := DefaultReplayPolicy()
	policy.ReprovisionOnRestart = false
	plan := registry.PlanReplay(PoolRestart{Provider: "docker_sandbox", SandboxUUID: "pool-1", ContainerID: "c"}, policy)
	if len(plan.Targets) != 0 || len(plan.Deferred) != 1 {
		t.Fatalf("expected a single deferral, got targets=%d deferred=%d", len(plan.Targets), len(plan.Deferred))
	}
	if plan.Deferred[0].Reason != "reprovision_on_restart_disabled" {
		t.Fatalf("unexpected reason %q", plan.Deferred[0].Reason)
	}
}

// TestCredentialRefRejectsLiteralSecret keeps the token out of references,
// reports and logs.
func TestCredentialRefRejectsLiteralSecret(t *testing.T) {
	// Shape-based fixtures only: no real token is ever needed here.
	secretish := "ghp_" + strings.Repeat("A", 36)
	bad := CredentialRef{Name: "github", EnvVars: []string{"GH_TOKEN"}, Source: secretish}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidCredentialRef) {
		t.Fatalf("expected a literal secret source to be rejected, got %v", err)
	}
	badEnv := CredentialRef{Name: "github", EnvVars: []string{"GH_TOKEN=" + secretish}, Source: "integration:github"}
	if err := badEnv.Validate(); !errors.Is(err, ErrInvalidCredentialRef) {
		t.Fatalf("expected a literal secret env var to be rejected, got %v", err)
	}
	traversal := CredentialRef{
		Name: "github", EnvVars: []string{"GH_TOKEN"},
		Source: "integration:github", HomeRelPath: "../../root/.config/gh/hosts.yml",
	}
	if err := traversal.Validate(); !errors.Is(err, ErrInvalidCredentialRef) {
		t.Fatalf("expected home traversal to be rejected, got %v", err)
	}
	if err := testGithubCred().Validate(); err != nil {
		t.Fatalf("valid reference rejected: %v", err)
	}
	if !LooksLikeSecretValue(secretish) || LooksLikeSecretValue("integration:github/main-bot") {
		t.Fatal("LooksLikeSecretValue misclassified a value")
	}
}

// TestSessionScopedEnvAndArgvGuard pins acceptance 2 and 3: a fresh shell is
// authenticated via GH_TOKEN/GITHUB_TOKEN before any gh config exists, and the
// token stays session-only (never in argv, never in the process list).
func TestSessionScopedEnvAndArgvGuard(t *testing.T) {
	identity := testIdentity("ws-1", "/home/ws-ws-1")
	creds := []CredentialRef{testGithubCred()}
	value := "value-from-integration" // stands in for a resolved credential
	resolver := func(_ WorkspaceIdentity, _ CredentialRef) (string, error) { return value, nil }

	env, err := SessionScopedEnv(identity, creds, resolver)
	if err != nil {
		t.Fatalf("session env: %v", err)
	}
	if env["GH_TOKEN"] != value || env["GITHUB_TOKEN"] != value {
		t.Fatalf("expected GH_TOKEN/GITHUB_TOKEN to be exported, got %v", sortedKeys(env))
	}

	spec := ShellSpec{Command: []string{"bash", "-l"}, Env: map[string]string{"GH_TOKEN": "stale"}}
	names := ApplySessionEnv(&spec, env)
	if strings.Join(names, ",") != "GH_TOKEN,GITHUB_TOKEN" {
		t.Fatalf("unexpected env names %v", names)
	}
	if spec.Env["GH_TOKEN"] != value {
		t.Fatal("expected the stale session token to be overwritten")
	}

	if err := CredentialArgvViolation(spec.Command, nil); err != nil {
		t.Fatalf("clean argv rejected: %v", err)
	}
	leaky := ShellSpec{Command: []string{"bash", "-c", "export GH_TOKEN=" + value}}
	if err := CredentialArgvViolation(leaky.Command, []string{value}); !errors.Is(err, ErrSecretInArgv) {
		t.Fatalf("expected the token in argv to be rejected, got %v", err)
	}

	if got := Redact(value); strings.Contains(got, value) {
		t.Fatalf("Redact leaked the value: %q", got)
	}
	for _, line := range append(SafeLogFields(identity, creds), (ReplayPlan{
		Provider: "docker_sandbox", SandboxUUID: "pool-1",
		Policy:  ReplayPolicy{ReprovisionOnRestart: true},
		Targets: []ReplayTarget{{Identity: identity, Credentials: creds, HomePaths: identity.CredentialPaths(creds)}},
	}).ReportLines()...) {
		if strings.Contains(line, value) {
			t.Fatalf("report/log line leaked the credential value: %q", line)
		}
	}

	if _, err := SessionScopedEnv(identity, creds, nil); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected a missing resolver to be reported, got %v", err)
	}
	empty := func(WorkspaceIdentity, CredentialRef) (string, error) { return "  ", nil }
	if _, err := SessionScopedEnv(identity, creds, empty); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected an empty value to be reported, got %v", err)
	}
}

// TestBindingValidationAndUnbind keeps the registry honest: no identity, no
// credentials or no per-agent HOME must not become a replay target.
func TestBindingValidationAndUnbind(t *testing.T) {
	registry := NewPoolRegistry()
	if err := registry.Bind("docker_sandbox", "pool-1", PoolBinding{Credentials: []CredentialRef{testGithubCred()}}); !errors.Is(err, ErrInvalidPoolBinding) {
		t.Fatalf("expected a missing identity to be rejected, got %v", err)
	}
	noHome := testBinding("ws-1", "")
	if err := registry.Bind("docker_sandbox", "pool-1", noHome); !errors.Is(err, ErrInvalidPoolBinding) {
		t.Fatalf("expected a missing per-agent HOME to be rejected, got %v", err)
	}
	noCreds := PoolBinding{Identity: testIdentity("ws-1", "/home/ws-ws-1")}
	if err := registry.Bind("docker_sandbox", "pool-1", noCreds); !errors.Is(err, ErrInvalidPoolBinding) {
		t.Fatalf("expected a credential-less binding to be rejected, got %v", err)
	}

	boundAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	binding := testBinding("ws-1", "/home/ws-ws-1")
	binding.BoundAt = boundAt
	if err := registry.Bind("docker_sandbox", "pool-1", binding); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Rebinding must not reset the replay bookkeeping.
	at := boundAt.Add(time.Minute)
	registry.RecordReplay("docker_sandbox", "pool-1", "ws-1", "container-a", at, DefaultReplayPolicy())
	rebind := testBinding("ws-1", "/home/ws-ws-1")
	if err := registry.Bind("docker_sandbox", "pool-1", rebind); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	stored := registry.Bindings("docker_sandbox", "pool-1")
	if len(stored) != 1 || !stored[0].BoundAt.Equal(boundAt) || !stored[0].LastReplayedAt.Equal(at) {
		t.Fatalf("rebinding reset replay bookkeeping: %+v", stored)
	}
	if len(registry.BindingsFor("ws-1")) != 1 {
		t.Fatal("expected BindingsFor to find the workspace")
	}
	registry.Unbind("docker_sandbox", "pool-1", "ws-1")
	if len(registry.Bindings("docker_sandbox", "pool-1")) != 0 {
		t.Fatal("expected Unbind to drop the binding")
	}
	if registry.RecordReplay("docker_sandbox", "pool-1", "ws-1", "container-a", at, DefaultReplayPolicy()) {
		t.Fatal("expected RecordReplay on an unbound workspace to report false")
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
