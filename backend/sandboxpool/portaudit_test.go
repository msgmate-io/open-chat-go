package sandboxpool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const ssSample = `State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process
LISTEN 0      128          0.0.0.0:20005      0.0.0.0:*
LISTEN 0      128             [::]:29999         [::]:*
LISTEN 0      128          0.0.0.0:20005      0.0.0.0:*
`

const netstatSample = `Active Internet connections (only servers)
Proto Recv-Q Send-Q Local Address           Foreign Address         State
tcp        0      0 0.0.0.0:20007           0.0.0.0:*               LISTEN
tcp6       0      0 :::31000                :::*                    LISTEN
`

func TestResolvePortBlockPrefersInjectedEnv(t *testing.T) {
	env := map[string]string{
		EnvWorkspacePortBase: "21000",
		EnvWorkspacePortLast: "21015",
	}
	block, ok := ResolvePortBlock(env, "workspace-a")
	if !ok {
		t.Fatal("expected a resolved block")
	}
	if block.Base != 21000 || block.Last != 21015 {
		t.Fatalf("unexpected block %+v", block)
	}
	if block.Size() != 16 || !block.Contains(21015) || block.Contains(21016) {
		t.Fatalf("unexpected block semantics %+v", block)
	}
	// The audit must agree with the injected env, not with the fallback.
	audit := AuditWorkspacePorts("workspace-a", block, []int{21000, 21015, 21016})
	if len(audit.Reserved) != 2 || len(audit.OutOfBlock) != 1 || audit.OutOfBlock[0] != 21016 {
		t.Fatalf("audit disagrees with injected env: %+v", audit)
	}
}

func TestResolvePortBlockBaseWithoutLast(t *testing.T) {
	block, ok := ResolvePortBlock(map[string]string{EnvWorkspacePortBase: "22000"}, "workspace-a")
	if !ok || block.Base != 22000 || block.Last != 22000+DefaultWorkspacePortSize-1 {
		t.Fatalf("unexpected block %+v ok=%t", block, ok)
	}
}

func TestResolvePortBlockFallbackIsStable(t *testing.T) {
	first, ok := ResolvePortBlock(nil, "workspace-a")
	if !ok {
		t.Fatal("expected fallback block")
	}
	again, _ := ResolvePortBlock(map[string]string{}, "workspace-a")
	if first != again {
		t.Fatalf("fallback not stable: %+v vs %+v", first, again)
	}
	distinct := map[PortBlock]bool{first: true}
	for _, key := range []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8"} {
		block, _ := ResolvePortBlock(nil, key)
		if !block.Valid() {
			t.Fatalf("invalid fallback block for %q", key)
		}
		distinct[block] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("fallback does not spread workspaces: %+v", distinct)
	}
}

func TestResolvePortBlockWithoutWorkspaceKey(t *testing.T) {
	if _, ok := ResolvePortBlock(nil, "   "); ok {
		t.Fatal("expected no block without a workspace key")
	}
}

func TestParseListeningPorts(t *testing.T) {
	got := ParseListeningPorts(ssSample)
	want := []int{20005, 29999}
	if len(got) != len(want) {
		t.Fatalf("ss parse = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ss parse = %v, want %v", got, want)
		}
	}
	got = ParseListeningPorts(netstatSample)
	want = []int{20007, 31000}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("netstat parse = %v, want %v", got, want)
	}
	if ports := ParseListeningPorts(""); len(ports) != 0 {
		t.Fatalf("empty output yielded %v", ports)
	}
}

func TestParseListeningPortsNoToolingMarker(t *testing.T) {
	if ports := ParseListeningPorts(PortAuditNoToolingMarker + "\n"); len(ports) != 0 {
		t.Fatalf("marker yielded %v", ports)
	}
}

func TestAuditWorkspacePortsNoListeners(t *testing.T) {
	audit := AuditWorkspacePorts("ws", PortBlock{Base: 20000, Last: 20015}, nil)
	if !audit.Available || audit.Drifted() || audit.Reason != PortAuditReasonNoListeners {
		t.Fatalf("unexpected audit %+v", audit)
	}
}

func TestPortAuditorWithoutRunnerIsNoOp(t *testing.T) {
	auditor := NewPortAuditor()
	audit := auditor.Audit(context.Background(), "ws", map[string]string{
		EnvWorkspacePortBase: "20000",
		EnvWorkspacePortLast: "20015",
	})
	if audit.Available || audit.Drifted() || audit.Reason != PortAuditReasonNoCollector {
		t.Fatalf("unexpected audit %+v", audit)
	}
	if status := auditor.Status(); status.Enabled {
		t.Fatalf("auditor without runner must not report enabled: %+v", status)
	}
}

func TestPortAuditorWithoutToolingIsNoOp(t *testing.T) {
	auditor := NewPortAuditor()
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return PortAuditNoToolingMarker + "\n", nil
	})
	audit := auditor.Audit(context.Background(), "ws", map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"})
	if audit.Available || audit.Drifted() || audit.Reason != PortAuditReasonNoTooling {
		t.Fatalf("unexpected audit %+v", audit)
	}
	if audit.Block().Base != 20000 {
		t.Fatalf("block lost: %+v", audit)
	}
}

func TestPortAuditorRunnerErrorIsNoOp(t *testing.T) {
	auditor := NewPortAuditor()
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return "", errors.New("exec unavailable")
	})
	audit := auditor.Audit(context.Background(), "ws", map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"})
	if audit.Available || audit.Reason != PortAuditReasonUnavailable {
		t.Fatalf("unexpected audit %+v", audit)
	}
	if status := auditor.Status(); status.LastError != PortAuditReasonUnavailable {
		t.Fatalf("unexpected status %+v", status)
	}
}

func TestPortAuditorReportsDriftAndRecurring(t *testing.T) {
	auditor := NewPortAuditor()
	auditor.SetPortDriftWarnStreak(2)
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return "LISTEN 0 128 0.0.0.0:20005 0.0.0.0:*\nLISTEN 0 128 0.0.0.0:29999 0.0.0.0:*\n", nil
	})
	env := map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"}

	first := auditor.Audit(context.Background(), "ws", env)
	if !first.Available || !first.Drifted() {
		t.Fatalf("drift not reported: %+v", first)
	}
	if len(first.Reserved) != 1 || first.Reserved[0] != 20005 || len(first.OutOfBlock) != 1 || first.OutOfBlock[0] != 29999 {
		t.Fatalf("unexpected split %+v", first)
	}
	if first.Recurring || first.WarnStreak != 1 {
		t.Fatalf("drift surfaced too early: %+v", first)
	}
	second := auditor.Audit(context.Background(), "ws", env)
	if !second.Recurring || second.WarnStreak != 2 {
		t.Fatalf("recurring drift not surfaced: %+v", second)
	}

	status := auditor.Status()
	if !status.Enabled || len(status.DriftedWorkspaces) != 1 || len(status.RecurringWorkspaces) != 1 {
		t.Fatalf("unexpected status %+v", status)
	}
	if len(status.Findings) != 1 || status.Findings[0].WorkspaceKey != "ws" {
		t.Fatalf("unexpected findings %+v", status.Findings)
	}

	// A clean audit clears the streak and the drift view.
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return "LISTEN 0 128 0.0.0.0:20005 0.0.0.0:*\n", nil
	})
	clean := auditor.Audit(context.Background(), "ws", env)
	if clean.Drifted() || clean.Recurring || clean.WarnStreak != 0 {
		t.Fatalf("drift not cleared: %+v", clean)
	}
	if status := auditor.Status(); len(status.DriftedWorkspaces) != 0 || len(status.RecurringWorkspaces) != 0 {
		t.Fatalf("status kept stale drift: %+v", status)
	}
}

func TestPortAuditorStatusIsSecretFree(t *testing.T) {
	auditor := NewPortAuditor()
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return "LISTEN 0 128 0.0.0.0:20005 0.0.0.0:*\n", nil
	})
	auditor.Audit(context.Background(), "ws", map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"})
	rendered := auditor.Status()
	if rendered.GeneratedAt == "" {
		t.Fatal("status missing timestamp")
	}
	if strings.Contains(rendered.Findings[0].WorkspaceKey, " ") {
		t.Fatalf("unexpected workspace key %q", rendered.Findings[0].WorkspaceKey)
	}
}

func TestPortAuditorSweepStopsOnCancelledContext(t *testing.T) {
	auditor := NewPortAuditor()
	calls := 0
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		calls++
		return "", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := auditor.Sweep(ctx, []PortAuditTarget{
		{WorkspaceKey: "a", Env: map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"}},
		{WorkspaceKey: "b", Env: map[string]string{EnvWorkspacePortBase: "20100", EnvWorkspacePortLast: "20115"}},
	})
	if len(results) != 0 || calls != 0 {
		t.Fatalf("cancelled sweep ran: results=%v calls=%d", results, calls)
	}
}

func TestPortAuditorStartLoopSweepsAndStops(t *testing.T) {
	auditor := NewPortAuditor()
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return "LISTEN 0 128 0.0.0.0:20005 0.0.0.0:*\n", nil
	})
	targets := func(context.Context) []PortAuditTarget {
		return []PortAuditTarget{{
			WorkspaceKey: "ws",
			Env:          map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"},
		}}
	}
	stop := auditor.StartLoop(context.Background(), time.Millisecond, targets)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := auditor.Last("ws"); ok {
			break
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatal("loop never audited a workspace")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
}

func TestPortAuditorStartLoopWithoutTargetsIsInert(t *testing.T) {
	auditor := NewPortAuditor()
	stop := auditor.StartLoop(context.Background(), time.Millisecond, nil)
	stop()
}

func TestGateStatusSurfacesPortAuditWithoutGating(t *testing.T) {
	gate := NewGate(nil)
	gate.SetDisabled(true)
	if status := gate.Status(false); status.PortAudit != nil {
		t.Fatalf("unattached gate reported a port audit: %+v", status.PortAudit)
	}

	auditor := NewPortAuditor()
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return "LISTEN 0 128 0.0.0.0:20005 0.0.0.0:*\nLISTEN 0 128 0.0.0.0:29999 0.0.0.0:*\n", nil
	})
	auditor.Audit(context.Background(), "ws", map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"})
	gate.SetPortAuditor(auditor)
	if gate.PortAuditor() != auditor {
		t.Fatal("auditor not attached")
	}

	status := gate.Status(false)
	if status.PortAudit == nil {
		t.Fatal("attached auditor not surfaced in the gate status")
	}
	if len(status.PortAudit.DriftedWorkspaces) != 1 || status.PortAudit.DriftedWorkspaces[0] != "ws" {
		t.Fatalf("drift not surfaced: %+v", status.PortAudit)
	}
	// Advisory only: the audit must not change the gate's own state.
	if status.Healthy || status.State != "unknown" {
		t.Fatalf("port audit changed the gate state: %+v", status)
	}

	gate.SetPortAuditor(nil)
	if status := gate.Status(false); status.PortAudit != nil {
		t.Fatalf("detached auditor still surfaced: %+v", status.PortAudit)
	}
}

func TestGateStatusPortAuditIsSecretFreeWhenDegraded(t *testing.T) {
	gate := NewGate(nil)
	gate.SetDisabled(true)
	auditor := NewPortAuditor()
	auditor.SetRunner(func(context.Context, string, string) (string, error) {
		return PortAuditNoToolingMarker + "\n", nil
	})
	auditor.Audit(context.Background(), "ws", map[string]string{EnvWorkspacePortBase: "20000", EnvWorkspacePortLast: "20015"})
	gate.SetPortAuditor(auditor)

	status := gate.Status(false)
	if status.PortAudit == nil || status.PortAudit.Enabled != true {
		t.Fatalf("unexpected port audit %+v", status.PortAudit)
	}
	if status.PortAudit.Unavailable != 1 || len(status.PortAudit.Findings) != 0 || len(status.PortAudit.DriftedWorkspaces) != 0 {
		t.Fatalf("degraded audit produced findings: %+v", status.PortAudit)
	}
	if status.PortAudit.LastError != PortAuditReasonNoTooling {
		t.Fatalf("unexpected last error %q", status.PortAudit.LastError)
	}
}
