package sandboxpool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// netplanview_test.go covers the ci#792 surfacing/provisioning half: item 1
// (the published port block on the transport), item 2 (the plan plus the composed
// network surface on the admin payload) and item 4 (per-workspace netns creation,
// fail closed on a host that cannot carry a namespace).

// supportedNetnsHost is the minimal host the netns policy accepts: user
// namespaces on, the pool's own CAP_NET_ADMIN and the slirp sidecar present.
func supportedNetnsHost() NetnsHostSupport {
	return NetnsHostSupport{
		UserNamespaces:    true,
		MaxUserNamespaces: 4096,
		CapNetAdmin:       true,
		SidecarPath:       "/usr/local/bin/slirp4netns",
	}
}

func blockedNetnsHost() NetnsHostSupport {
	host := supportedNetnsHost()
	host.CapNetAdmin = false
	host.Blockers = []string{"CAP_NET_ADMIN is not in the effective set"}
	return host
}

func TestPoolNetworkViewCarriesTransportOrReason(t *testing.T) {
	now := time.Now()
	view := PoolNetworkViewFromStatus(NetworkPlanStatus{Mode: NetworkModeShared}, now)
	if view.GeneratedAt == "" {
		t.Fatal("view must be timestamped")
	}
	if view.Transport == nil && strings.TrimSpace(view.TransportError) == "" {
		t.Fatal("an unpublished block must carry a reason, not a half-filled transport")
	}
	if view.Transport == nil {
		lines := strings.Join(view.ReportLines(), "\n")
		if !strings.Contains(lines, "transport=unpublished reason=") {
			t.Fatalf("report must state the unpublished block: %s", lines)
		}
	}
}

func TestPoolNetworkViewSurfacesNetnsRefusal(t *testing.T) {
	now := time.Now()
	status := NetworkPlanStatus{
		Mode: NetworkModeNetns,
		Workspaces: []WorkspaceNetworkPlan{{
			WorkspaceUUID: "3f1c0f9e-1b3d-4a2c-9d4e-1b3d4a2c9d4e",
		}},
	}
	view := PoolNetworkViewFromStatus(status, now)
	if view.NetnsHost.Supported() {
		t.Fatal("the resolved default host must not be assumed capable in this environment")
	}
	lines := strings.Join(view.ReportLines(), "\n")
	if strings.Contains(lines, "secret") || strings.Contains(lines, "token") {
		t.Fatal("the composed view must stay credential free")
	}
	if len(view.Netns) > 0 && len(view.NetnsErrors) > 0 {
		t.Fatal("a workspace is either provisioned or refused, never both")
	}
}

func TestPoolNetworkViewReportsNetnsErrorsPerWorkspace(t *testing.T) {
	now := time.Now()
	status := NetworkPlanStatus{
		Mode: NetworkModeNetns,
		Workspaces: []WorkspaceNetworkPlan{{
			WorkspaceUUID: "", // the policy refuses an empty identity
		}},
	}
	view := PoolNetworkViewFromStatus(status, now)
	if len(view.NetnsErrors) != 1 {
		t.Fatalf("the refused namespace must be surfaced per workspace, got %v", view.NetnsErrors)
	}
	if !strings.Contains(view.NetnsErrors[0], "workspace ") {
		t.Fatalf("refusal must name the workspace: %s", view.NetnsErrors[0])
	}
	if !strings.Contains(strings.Join(view.ReportLines(), "\n"), "netns_refused=") {
		t.Fatalf("the report must expose the refusal: %v", view.ReportLines())
	}
}

func TestNetnsProvisionerFailsClosedWithoutRunner(t *testing.T) {
	p := NewNetnsProvisioner()
	if p.Runner() {
		t.Fatal("a fresh provisioner must be inert")
	}
	_, err := p.Provision(context.Background(),
		WorkspaceNetworkPlan{WorkspaceUUID: "3f1c0f9e-1b3d-4a2c-9d4e-1b3d4a2c9d4e"},
		supportedNetnsHost(), time.Now())
	if !errors.Is(err, ErrWorkspaceIsolationUnavailable) {
		t.Fatalf("an unattached pool must refuse the sensitive scope, got %v", err)
	}
}

func TestNetnsProvisionerRunsEveryCommandOfThePlan(t *testing.T) {
	p := NewNetnsProvisioner()
	var ran []string
	p.SetRunner(func(_ context.Context, _ WorkspaceNetns, command string) error {
		ran = append(ran, command)
		return nil
	})
	if !p.Runner() {
		t.Fatal("SetRunner must attach the exec seam")
	}
	netns, err := p.Provision(context.Background(),
		WorkspaceNetworkPlan{WorkspaceUUID: "3f1c0f9e-1b3d-4a2c-9d4e-1b3d4a2c9d4e"},
		supportedNetnsHost(), time.Now())
	if err != nil {
		t.Fatalf("a supported host must provision: %v", err)
	}
	if len(ran) != len(netns.Commands) || len(ran) == 0 {
		t.Fatalf("every rendered step must run exactly once: ran=%d rendered=%d", len(ran), len(netns.Commands))
	}
	if netns.Name == "" || netns.Path == "" {
		t.Fatalf("the provisioned namespace must carry its name and path: %+v", netns)
	}
}

func TestNetnsProvisionerRunsNothingForARefusedPlan(t *testing.T) {
	p := NewNetnsProvisioner()
	var ran []string
	p.SetRunner(func(_ context.Context, _ WorkspaceNetns, command string) error {
		ran = append(ran, command)
		return nil
	})
	_, err := p.Provision(context.Background(), WorkspaceNetworkPlan{WorkspaceUUID: "  "}, supportedNetnsHost(), time.Now())
	if !errors.Is(err, ErrInvalidWorkspaceUUID) {
		t.Fatalf("want ErrInvalidWorkspaceUUID, got %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("a refused plan must not run any command: %v", ran)
	}
}

func TestNetnsProvisionerStopsOnAStepFailure(t *testing.T) {
	p := NewNetnsProvisioner()
	var ran int
	p.SetRunner(func(_ context.Context, _ WorkspaceNetns, _ string) error {
		ran++
		return errors.New("exec seam is down")
	})
	_, err := p.Provision(context.Background(),
		WorkspaceNetworkPlan{WorkspaceUUID: "3f1c0f9e-1b3d-4a2c-9d4e-1b3d4a2c9d4e"},
		supportedNetnsHost(), time.Now())
	if !errors.Is(err, ErrWorkspaceIsolationUnavailable) {
		t.Fatalf("a failed step must refuse the workspace, got %v", err)
	}
	if ran != 1 {
		t.Fatalf("provisioning must stop at the first failed step, ran=%d", ran)
	}
}

func TestNetnsProvisionerRefusesUnsupportedHost(t *testing.T) {
	p := NewNetnsProvisioner()
	var ran []string
	p.SetRunner(func(_ context.Context, _ WorkspaceNetns, command string) error {
		ran = append(ran, command)
		return nil
	})
	_, err := p.Provision(context.Background(),
		WorkspaceNetworkPlan{WorkspaceUUID: "3f1c0f9e-1b3d-4a2c-9d4e-1b3d4a2c9d4e"},
		blockedNetnsHost(), time.Now())
	if !errors.Is(err, ErrWorkspaceIsolationUnavailable) {
		t.Fatalf("an incapable host must fail closed, got %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("no command may run on an incapable host: %v", ran)
	}
}

func TestNetnsProvisionerProvisionAllReportsPerWorkspace(t *testing.T) {
	p := NewNetnsProvisioner()
	p.SetRunner(func(_ context.Context, _ WorkspaceNetns, _ string) error { return nil })
	now := time.Now()
	status := NetworkPlanStatus{
		Mode: NetworkModeNetns,
		Workspaces: []WorkspaceNetworkPlan{
			{WorkspaceUUID: "3f1c0f9e-1b3d-4a2c-9d4e-1b3d4a2c9d4e"},
			{WorkspaceUUID: ""},
		},
	}
	netnsList, errs := p.ProvisionAll(context.Background(), status, supportedNetnsHost(), now)
	if len(netnsList) != 1 {
		t.Fatalf("the valid workspace must be provisioned, got %+v", netnsList)
	}
	if len(errs) != 1 {
		t.Fatalf("the refused workspace must be reported, got %v", errs)
	}
}

func TestNetnsProvisionerReleaseWithoutRunnerAndWithoutName(t *testing.T) {
	p := NewNetnsProvisioner()
	if err := p.Release(context.Background(), WorkspaceNetns{Name: "oc-ws-1"}); !errors.Is(err, ErrWorkspaceIsolationUnavailable) {
		t.Fatalf("release without a seam must refuse, got %v", err)
	}
	p.SetRunner(func(_ context.Context, _ WorkspaceNetns, _ string) error { return nil })
	if err := p.Release(context.Background(), WorkspaceNetns{}); !errors.Is(err, ErrInvalidWorkspaceUUID) {
		t.Fatalf("release without a namespace name must refuse, got %v", err)
	}
	if err := p.Release(context.Background(), WorkspaceNetns{Name: "oc-ws-1"}); err != nil {
		t.Fatalf("release with a name and a seam must succeed: %v", err)
	}
}

func TestDefaultNetnsProvisionerIsSharedAndInert(t *testing.T) {
	if DefaultNetnsProvisioner() != DefaultNetnsProvisioner() {
		t.Fatal("the process-wide provisioner must be stable")
	}
}
