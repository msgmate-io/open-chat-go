package sandboxpool

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeConn is a no-op net.Conn, so a dialer can "succeed" without a network.
type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

// healthyGate returns a gate with no environment influence and a fake pool
// network: every DNS name resolves and every dial succeeds.
func healthyGate(t *testing.T) *Gate {
	t.Helper()
	gate := NewGate(nil)
	gate.SetDisabled(false)
	gate.SetStrict(false)
	gate.SetLocalProbe(false)
	gate.SetReportTTL(DefaultReportTTL)
	gate.SetProbeTimeout(time.Second)
	gate.SetRecycleAfterFailures(DefaultRecycleAfterFailures)
	gate.SetNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })
	gate.SetProbers(
		func(ctx context.Context, host string) ([]string, error) {
			return []string{"10.0.0.1"}, nil
		},
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			return fakeConn{}, nil
		},
	)
	return gate
}

func TestGateIsAdvisoryBeforeAnyReport(t *testing.T) {
	gate := healthyGate(t)

	// Before a report exists the gate cannot say anything about the pool's
	// egress, so it must not block leases in the default (advisory) mode.
	if err := gate.AdmitLease(context.Background(), "chat-1"); err != nil {
		t.Fatalf("advisory gate blocked a lease: %v", err)
	}
	status := gate.Status(false)
	if status.Armed {
		t.Fatalf("gate reported armed without a report: %+v", status)
	}
	if status.State != "unknown" {
		t.Fatalf("expected unknown state before a report, got %q", status.State)
	}
	if status.AdmittedLeases != 1 {
		t.Fatalf("expected 1 admitted lease, got %d", status.AdmittedLeases)
	}
	if status.Disabled || status.Strict {
		t.Fatalf("gate unexpectedly strict/disabled: %+v", status)
	}
}

func TestStrictGateBlocksLeasesWithoutReport(t *testing.T) {
	gate := healthyGate(t)
	gate.SetStrict(true)

	err := gate.AdmitLease(context.Background(), "chat-1")
	if !errors.Is(err, ErrPoolEgressUnhealthy) {
		t.Fatalf("expected ErrPoolEgressUnhealthy, got %v", err)
	}
	if !IsPoolEgressFailure(err) {
		t.Fatalf("IsPoolEgressFailure should classify %v", err)
	}
	if !strings.Contains(err.Error(), "chat-1") {
		t.Fatalf("blocked lease error should name the subject: %v", err)
	}
	if status := gate.Status(false); status.BlockedLeases != 1 || status.Armed {
		t.Fatalf("expected 1 blocked lease and no report: %+v", status)
	}
}

func TestDisabledGateNeverBlocks(t *testing.T) {
	gate := healthyGate(t)
	gate.SetStrict(true)
	gate.SetDisabled(true)

	if err := gate.AdmitLease(context.Background(), "chat-1"); err != nil {
		t.Fatalf("disabled gate blocked a lease: %v", err)
	}
}

func TestProbePassesForHealthyPool(t *testing.T) {
	gate := healthyGate(t)

	report := gate.Probe(context.Background())
	if !report.Healthy() {
		t.Fatalf("expected healthy report, got %s", report.Summary())
	}
	if len(report.Targets) != len(DefaultEgressTargets) {
		t.Fatalf("expected %d probes, got %d", len(DefaultEgressTargets), len(report.Targets))
	}
	seen := map[string]bool{}
	for _, target := range report.Targets {
		seen[target.Target] = true
		if target.Remediation != "" {
			t.Fatalf("healthy probe %s carried a remediation: %q", target.Target, target.Remediation)
		}
	}
	for _, want := range []string{"dns:kubernetes.default.svc.cluster.local", "tcp:github.com:443", "tcp:api.github.com:443", "tcp:uploads.github.com:443"} {
		if !seen[want] {
			t.Fatalf("default targets should include %s, got %v", want, seen)
		}
	}

	if err := gate.AdmitLease(context.Background(), "chat-1"); err != nil {
		t.Fatalf("healthy pool must pass the gate: %v", err)
	}
}

func TestFailingDNSProbeMarksPoolUnhealthy(t *testing.T) {
	gate := healthyGate(t)
	gate.SetProbers(
		func(ctx context.Context, host string) ([]string, error) {
			if host == "kubernetes.default.svc.cluster.local" {
				return nil, errors.New("lookup kubernetes.default.svc.cluster.local: no such host")
			}
			return []string{"10.0.0.1"}, nil
		},
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			return fakeConn{}, nil
		},
	)

	report, err := gate.ProbeAndAdmit(context.Background())
	if !errors.Is(err, ErrPoolEgressUnhealthy) {
		t.Fatalf("expected ErrPoolEgressUnhealthy, got %v", err)
	}
	if report.Healthy() {
		t.Fatalf("report with a broken DNS probe must be unhealthy")
	}
	summary := report.Summary()
	if !strings.Contains(summary, "dns:kubernetes.default.svc.cluster.local") || !strings.Contains(summary, "no such host") {
		t.Fatalf("summary should name the failing target and its error: %s", summary)
	}
	remediation := report.Remediation()
	if len(remediation) != 1 || !strings.Contains(remediation[0], "CoreDNS") {
		t.Fatalf("expected a CoreDNS remediation hint, got %v", remediation)
	}

	// The pool must no longer be handed out.
	leaseErr := gate.AdmitLease(context.Background(), "chat-1")
	if !errors.Is(leaseErr, ErrPoolEgressUnhealthy) {
		t.Fatalf("unhealthy pool handed out a lease: %v", leaseErr)
	}
	status := gate.Status(false)
	if status.Healthy || status.State != "unhealthy" {
		t.Fatalf("expected unhealthy status, got %+v", status)
	}
	if status.Report == nil || !strings.Contains(status.LastError, "dns:kubernetes.default.svc.cluster.local") {
		t.Fatalf("gate should expose the failing probe: %+v", status)
	}
}

func TestEgressFailureIsUnhealthyAndRecycled(t *testing.T) {
	gate := healthyGate(t)
	gate.SetProbers(
		func(ctx context.Context, host string) ([]string, error) { return []string{"10.0.0.1"}, nil },
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("dial tcp: i/o timeout")
		},
	)

	report := gate.Probe(context.Background())
	for i := 1; i <= DefaultRecycleAfterFailures; i++ {
		err := gate.Admit(report)
		if !errors.Is(err, ErrPoolEgressUnhealthy) {
			t.Fatalf("admit %d: expected ErrPoolEgressUnhealthy, got %v", i, err)
		}
		status := gate.Status(false)
		if status.ConsecutiveFailed != i {
			t.Fatalf("admit %d: expected failure streak %d, got %d", i, i, status.ConsecutiveFailed)
		}
		if status.RecycleRequested != (i >= DefaultRecycleAfterFailures) {
			t.Fatalf("admit %d: unexpected recycle flag %v", i, status.RecycleRequested)
		}
	}
	if hints := gate.Status(false).ReportRemediation; len(hints) == 0 || !strings.Contains(hints[0], "ci#497") {
		t.Fatalf("expected a NAT/egress remediation hint mentioning ci#497, got %v", hints)
	}
	if err := gate.AdmitLease(context.Background(), "chat-2"); !errors.Is(err, ErrPoolEgressUnhealthy) {
		t.Fatalf("recycled pool still handed out a lease: %v", err)
	}
}

func TestRecoveryClearsFailureState(t *testing.T) {
	gate := healthyGate(t)
	bad := Report{CheckedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Targets: []ProbeResult{{Name: "github", Kind: KindTCP, Target: "tcp:github.com:443"}}}
	if err := gate.Admit(bad); !errors.Is(err, ErrPoolEgressUnhealthy) {
		t.Fatalf("expected unhealthy admit, got %v", err)
	}
	if gate.Status(false).RecycleRequested {
		t.Fatalf("a single failure must not request a recycle yet (threshold %d)", DefaultRecycleAfterFailures)
	}

	good := Report{CheckedAt: time.Date(2026, 1, 2, 3, 5, 5, 0, time.UTC), Targets: []ProbeResult{{Name: "github", Kind: KindTCP, Target: "tcp:github.com:443", OK: true}}}
	if err := gate.Admit(good); err != nil {
		t.Fatalf("healthy report rejected: %v", err)
	}
	if err := gate.AdmitLease(context.Background(), "chat-3"); err != nil {
		t.Fatalf("recovered pool blocked a lease: %v", err)
	}
	status := gate.Status(false)
	if !status.Healthy || status.State != "healthy" || status.ConsecutiveFailed != 0 || status.RecycleRequested {
		t.Fatalf("recovery did not reset the gate: %+v", status)
	}
	if status.HealthySince == "" {
		t.Fatalf("healthy_since should be set after a healthy report")
	}
}

func TestFailingReportKeepsBlockingWhenStale(t *testing.T) {
	gate := healthyGate(t)
	gate.SetReportTTL(time.Minute)
	gate.SetNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })
	bad := Report{
		CheckedAt: time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC),
		Targets:   []ProbeResult{{Name: "github", Kind: KindTCP, Target: "tcp:github.com:443"}},
	}
	if err := gate.Admit(bad); !errors.Is(err, ErrPoolEgressUnhealthy) {
		t.Fatalf("expected unhealthy admit, got %v", err)
	}
	if !gate.Status(false).ReportStale {
		t.Fatalf("old report should be reported stale")
	}
	if err := gate.AdmitLease(context.Background(), "chat-4"); !errors.Is(err, ErrPoolEgressUnhealthy) {
		t.Fatalf("stale failing report must keep blocking: %v", err)
	}
}

func TestStaleHealthyReportIsStillAdmitted(t *testing.T) {
	gate := healthyGate(t)
	gate.SetReportTTL(time.Minute)
	gate.SetNow(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) })
	good := Report{
		CheckedAt: time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC),
		Targets:   []ProbeResult{{Name: "github", Kind: KindTCP, Target: "tcp:github.com:443", OK: true}},
	}
	if err := gate.Admit(good); err != nil {
		t.Fatalf("healthy report rejected: %v", err)
	}
	if !gate.Status(false).ReportStale {
		t.Fatalf("report should be reported stale")
	}
	if err := gate.AdmitLease(context.Background(), "chat-5"); err != nil {
		t.Fatalf("a last-known-good report must not block leases: %v", err)
	}
}

func TestLocalProbeBlockedPoolStaysBlocked(t *testing.T) {
	gate := healthyGate(t)
	gate.SetLocalProbe(true)
	gate.SetStrict(true)
	gate.SetProbers(
		func(ctx context.Context, host string) ([]string, error) {
			return nil, errors.New("server misbehaving")
		},
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("connect: network is unreachable")
		},
	)

	// No report yet and local probe enabled: the lease path probes and fails
	// closed.
	if err := gate.AdmitLease(context.Background(), "chat-6"); !errors.Is(err, ErrPoolEgressUnhealthy) {
		t.Fatalf("expected the local probe to block the lease, got %v", err)
	}
	status := gate.Status(false)
	if status.Healthy || !status.Armed {
		t.Fatalf("local probe should have admitted a failing report: %+v", status)
	}
	if status.LocalProbe != true {
		t.Fatalf("status should report the local probe mode")
	}
}

func TestLocalProbeHealthyPoolPasses(t *testing.T) {
	gate := healthyGate(t)
	gate.SetLocalProbe(true)

	if err := gate.AdmitLease(context.Background(), "chat-7"); err != nil {
		t.Fatalf("local probe should pass for a healthy pool: %v", err)
	}
	if status := gate.Status(false); !status.Healthy || status.AdmittedLeases != 1 {
		t.Fatalf("unexpected status after a local probe: %+v", status)
	}
}

func TestAdmitRejectsEmptyReport(t *testing.T) {
	gate := healthyGate(t)
	err := gate.Admit(Report{})
	if !errors.Is(err, ErrPoolEgressInvalidReport) {
		t.Fatalf("expected ErrPoolEgressInvalidReport, got %v", err)
	}
	if gate.Status(false).Armed {
		t.Fatalf("an empty report must not arm the gate")
	}
}

func TestParseReport(t *testing.T) {
	raw := []byte(`{"pool":"sandbox-1","source":"pool-pod","checked_at":"2026-01-02T03:04:05Z","duration_ms":12,"targets":[{"name":"github","kind":"tcp","target":"tcp:github.com:443","ok":true,"duration_ms":5}]}`)
	report, err := ParseReport(raw)
	if err != nil {
		t.Fatalf("ParseReport failed: %v", err)
	}
	if !report.Healthy() || report.Pool != "sandbox-1" || len(report.Targets) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}

	if _, err := ParseReport([]byte("not json")); !errors.Is(err, ErrPoolEgressInvalidReport) {
		t.Fatalf("expected ErrPoolEgressInvalidReport for garbage, got %v", err)
	}
	if _, err := ParseReport([]byte(`{"targets":[]}`)); !errors.Is(err, ErrPoolEgressInvalidReport) {
		t.Fatalf("expected ErrPoolEgressInvalidReport for an empty probe list, got %v", err)
	}
}

func TestTargetsFromEnv(t *testing.T) {
	targets := TargetsFromEnv("dns:cluster.local, tcp:github.com, tcp:api.github.com:8443")
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %+v", targets)
	}
	if targets[0].Kind != KindDNS || targets[0].Host != "cluster.local" {
		t.Fatalf("unexpected dns target: %+v", targets[0])
	}
	if targets[1].Kind != KindTCP || targets[1].Port != 443 {
		t.Fatalf("bare tcp target should default to :443, got %+v", targets[1])
	}
	if targets[2].Addr() != "api.github.com:8443" {
		t.Fatalf("unexpected tcp addr: %s", targets[2].Addr())
	}
	if got := TargetsFromEnv("  "); got != nil {
		t.Fatalf("empty value should yield nil, got %+v", got)
	}
	if got := TargetsFromEnv("nonsense"); got != nil {
		t.Fatalf("unparsable value should yield nil, got %+v", got)
	}
}

func TestGateEnvLevers(t *testing.T) {
	t.Setenv(EnvStrict, "true")
	t.Setenv(EnvDisabled, "0")
	t.Setenv(EnvProbeLocally, "yes")
	t.Setenv(EnvProbeTimeoutMS, "1500")
	t.Setenv(EnvReportTTLMS, "60000")
	t.Setenv(EnvRecycleAfterFails, "2")
	t.Setenv(EnvTargets, "tcp:github.com:443")

	gate := NewGate(nil)
	status := gate.Status(false)
	if !status.Strict || status.Disabled || !status.LocalProbe {
		t.Fatalf("env levers not applied: %+v", status)
	}
	if status.ProbeTimeoutMS != 1500 || status.ReportTTLMS != 60000 {
		t.Fatalf("durations not applied: %+v", status)
	}
	if len(status.Targets) != 1 || status.Targets[0] != "tcp:github.com:443" {
		t.Fatalf("target override not applied: %+v", status.Targets)
	}
	// The failure streak lever has to be honoured: two failing reports recycle.
	bad := Report{Targets: []ProbeResult{{Name: "github", Kind: KindTCP, Target: "tcp:github.com:443"}}}
	_ = gate.Admit(bad)
	if gate.Status(false).RecycleRequested {
		t.Fatalf("first failure must not yet request a recycle (threshold 2)")
	}
	_ = gate.Admit(bad)
	if !gate.Status(false).RecycleRequested {
		t.Fatalf("second failure should request a recycle (threshold 2)")
	}
}

func TestStatusReportsTargetsAndRemediation(t *testing.T) {
	gate := healthyGate(t)
	report := gate.Probe(context.Background())
	_ = gate.Admit(report)

	status := gate.Status(false)
	if len(status.Targets) != len(DefaultEgressTargets) {
		t.Fatalf("status should list the probed targets: %+v", status.Targets)
	}
	if status.GeneratedAt == "" {
		t.Fatalf("status should carry a generation timestamp")
	}
	if status.ProbeTimeoutMS <= 0 || status.ReportTTLMS <= 0 {
		t.Fatalf("status should carry the gate policy: %+v", status)
	}
}
