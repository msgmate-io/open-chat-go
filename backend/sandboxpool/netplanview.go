package sandboxpool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// This file closes the surfacing half of the ci#792 batch of the ci#546
// shared-sandbox networking design, and adds the missing provisioning seam of
// item 4. The policy (netisolation.go), the plan status (netplanstatus.go), the
// transport declaration (transport.go), the netns host check (netnsprovider.go),
// the cgroup guard (cgroup.go) and the exhaustion signal (exhaustion.go) all
// exist, but nothing composed them into the one view an operator needs, nothing
// ever invoked the rendered namespace provisioning, and a chat-lead had no way
// to see whether the port block is actually published. So:
//
//   - PoolNetworkView composes plan + transport + netns host + per-workspace
//     namespaces + exhaustion signal into one read-only bundle (items 1/2/4/5),
//   - NetnsProvisioner is the executor that finally creates the per-workspace
//     namespaces of netns mode (item 4), failing closed exactly like the policy.
//
// Everything here is read-only with respect to leases, ports and cgroups, and it
// carries ports, aliases, namespace names and env lever names only - never a
// secret, a kubeconfig or user data.

// PoolNetworkView is the composed operator view of the pool network surface: the
// plan the lease path resolves (item 2), the declaration that publishes the port
// block on the transport (item 1), the fail-closed host check and the
// per-workspace namespaces of netns mode (item 4), and the distinct exhaustion
// signal of a full pool (item 5).
type PoolNetworkView struct {
	GeneratedAt string `json:"generated_at"`
	// Plan is the resolved policy/allocator state (mode, block geometry, live
	// blocks, workspace ceiling, exhaustion condition).
	Plan NetworkPlanStatus `json:"plan"`
	// Transport is the publish declaration of the pool port block: the mapping
	// per workspace block plus its reachability probe. It is nil when no block is
	// published, so the operator reads TransportError instead of a half-filled
	// contract.
	Transport *PoolTransport `json:"transport,omitempty"`
	// TransportError is why nothing is published (unattached allocator, invalid
	// transport lever, unusable block geometry).
	TransportError string `json:"transport_error,omitempty"`
	// NetnsHost is the resolved, fail-closed host capability check of netns mode.
	NetnsHost NetnsHostSupport `json:"netns_host"`
	// Netns are the rendered namespaces of the live workspaces in netns mode.
	Netns []WorkspaceNetns `json:"netns,omitempty"`
	// NetnsErrors are the per-workspace refusals: a workspace the pool would not
	// give a namespace (unsupported host, invalid uuid). Non-empty here is the
	// operator-visible reason a sensitive scope was refused rather than silently
	// downgraded to shared mode.
	NetnsErrors []string `json:"netns_errors,omitempty"`
	// Exhaustion is the distinct full-pool signal (item 5); nil while blocks are
	// free, so the lease path can spill to another pool pod on a non-nil one.
	Exhaustion *PoolExhaustionSignal `json:"exhaustion,omitempty"`
	// Notes are the human-readable caveats of the composed view. Never a secret.
	Notes []string `json:"notes,omitempty"`
}

// ResolvePoolNetworkView composes the view from the process-wide plan. It never
// allocates or releases a block, never creates a namespace or a cgroup slice, and
// never gates a lease: a refused namespace shows up as a NetnsErrors entry (the
// same fail-closed reason the lease path raises), not as a mutation here.
func ResolvePoolNetworkView(now time.Time) PoolNetworkView {
	return PoolNetworkViewFromStatus(ResolveNetworkPlanStatus(now), now)
}

// PoolNetworkViewFromStatus composes the view on an already rendered plan status,
// so an admin render resolves the plan exactly once and cannot show a plan that
// contradicts the transport/exhaustion blocks beside it.
func PoolNetworkViewFromStatus(status NetworkPlanStatus, now time.Time) PoolNetworkView {
	view := PoolNetworkView{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Plan:        status,
		NetnsHost:   ResolveNetnsHostSupport(),
		Notes: []string{
			"the transport declaration publishes one port range plus a reachability probe per workspace block (ci#792 item 1), so a chat-lead dials the block the workspace actually owns instead of guessing a loopback alias",
			"netns mode (ci#792 item 4) is fail-closed: a host that cannot carry a namespace refuses the sensitive scope with ErrWorkspaceIsolationUnavailable and is never downgraded to shared mode",
			"a full pool (ci#792 item 5) reports the distinct exhaustion signal instead of a generic allocation error, so the lease path can spill to a new pool pod or fall back to a dedicated sandbox",
			"read-only view: nothing here allocates a block, creates a namespace or releases a lease, and it carries ports, aliases and env lever names only",
		},
	}

	transport, err := ResolvePoolTransportFromEnv(now)
	if err != nil {
		view.TransportError = fmt.Sprintf("the pool port block is not published: %v", err)
	} else {
		view.Transport = &transport
	}

	if signal, ok := ExhaustionSignalFromStatus(status, now); ok {
		view.Exhaustion = &signal
	}

	if status.Mode == NetworkModeNetns {
		for _, plan := range status.Workspaces {
			netns, err := ResolveWorkspaceNetns(plan, view.NetnsHost, now)
			if err != nil {
				view.NetnsErrors = append(view.NetnsErrors, fmt.Sprintf("workspace %s: %v", plan.WorkspaceUUID, err))
				continue
			}
			view.Netns = append(view.Netns, netns)
		}
		if len(view.NetnsErrors) > 0 {
			view.Notes = append(view.Notes, fmt.Sprintf(
				"%d live workspace(s) hold a block but no namespace: the pool refused them instead of downgrading, fix the levers in netns_host.blockers", len(view.NetnsErrors)))
		}
	} else {
		view.Notes = append(view.Notes, fmt.Sprintf(
			"mode %s does not provision per-workspace namespaces; netns mode is selected with %s=%s (and is required for the isolation scopes)", status.Mode, EnvNetworkMode, NetworkModeNetns))
	}

	return view
}

// ReportLines renders the composed view for the CI log and the admin health
// path: one line per surface, an explicit "unpublished" line when the transport
// declaration is missing (so an unreachable workspace is not mistaken for a
// broken dev server), and one line per netns refusal.
func (v PoolNetworkView) ReportLines() []string {
	lines := []string{fmt.Sprintf(
		"sandbox_pool_network_view generated_at=%s mode=%s slots=%d active=%d free=%d exhausted=%t netns_available=%t",
		v.GeneratedAt, v.Plan.Mode, v.Plan.Slots, v.Plan.ActiveWorkspaces, v.Plan.FreeSlots, v.Plan.Exhausted, v.NetnsHost.Supported())}
	if v.Transport != nil {
		lines = append(lines, fmt.Sprintf(
			"sandbox_pool_network_view transport kind=%s service=%s pool_host=%s mappings=%d health_checks=%d",
			v.Transport.Kind, v.Transport.Service, v.Transport.PoolHost, len(v.Transport.Mappings), len(v.Transport.HealthChecks)))
	} else {
		lines = append(lines, "sandbox_pool_network_view transport=unpublished reason="+strings.TrimSpace(v.TransportError))
	}
	if v.Exhaustion != nil {
		lines = append(lines, fmt.Sprintf(
			"sandbox_pool_network_view exhaustion kind=%s mode=%s pool=%s spill=%s slots=%d active=%d",
			v.Exhaustion.Kind, v.Exhaustion.Mode, v.Exhaustion.Pool, v.Exhaustion.Spill, v.Exhaustion.Slots, v.Exhaustion.ActiveWorkspaces))
	}
	for _, errLine := range v.NetnsErrors {
		lines = append(lines, "sandbox_pool_network_view netns_refused="+errLine)
	}
	return lines
}

// NetnsProvisionRunner executes one provisioning step inside the pool.
// Implementations are the in-pod exec seam (SSH/kubectl exec), mirroring the
// PortAuditor runner. The workspace carries the namespace name, the loopback and
// the port block, so a runner never has to re-derive them.
type NetnsProvisionRunner func(ctx context.Context, workspace WorkspaceNetns, command string) error

// NetnsProvisioner creates and drops the per-workspace network namespaces of
// netns mode (ci#792 item 4, the provisioning half). Without a runner it refuses
// every workspace with ErrWorkspaceIsolationUnavailable, so a pool that has not
// wired the in-pod exec seam fails closed instead of reporting a namespace it
// never created.
type NetnsProvisioner struct {
	mu     sync.Mutex
	runner NetnsProvisionRunner
}

// NewNetnsProvisioner returns a provisioner without a runner: inert and
// fail-closed, not an error.
func NewNetnsProvisioner() *NetnsProvisioner { return &NetnsProvisioner{} }

var (
	defaultNetnsProvisionerOnce sync.Once
	defaultNetnsProvisioner     *NetnsProvisioner
)

// DefaultNetnsProvisioner returns the process-wide provisioner the pool start
// path and the lease path share.
func DefaultNetnsProvisioner() *NetnsProvisioner {
	defaultNetnsProvisionerOnce.Do(func() { defaultNetnsProvisioner = NewNetnsProvisioner() })
	return defaultNetnsProvisioner
}

// SetRunner wires the in-pod exec seam. It is the only requirement for the
// provisioner to create a namespace; passing nil makes it inert again.
func (p *NetnsProvisioner) SetRunner(runner NetnsProvisionRunner) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runner = runner
}

// Runner reports whether an exec seam is attached.
func (p *NetnsProvisioner) Runner() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runner != nil
}

// attachedRunner reads the exec seam under the lock; it is the only way the
// provisioning paths read the field.
func (p *NetnsProvisioner) attachedRunner() NetnsProvisionRunner {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runner
}

// Provision renders and executes the namespace of one workspace. It fails closed
// in three places: an unsupported host (via ResolveWorkspaceNetns), a missing
// runner and a failed step all surface ErrWorkspaceIsolationUnavailable, so a
// sensitive scope is refused rather than downgraded to shared mode.
func (p *NetnsProvisioner) Provision(ctx context.Context, plan WorkspaceNetworkPlan, host NetnsHostSupport, now time.Time) (WorkspaceNetns, error) {
	netns, err := ResolveWorkspaceNetns(plan, host, now)
	if err != nil {
		return WorkspaceNetns{}, err
	}
	runner := p.attachedRunner()
	if runner == nil {
		return WorkspaceNetns{}, fmt.Errorf(
			"%w: no provisioning runner is attached to the pool, so the namespace of workspace %s is not created",
			ErrWorkspaceIsolationUnavailable, netns.WorkspaceUUID)
	}
	for index, command := range netns.Commands {
		if err := runner(ctx, netns, command); err != nil {
			return WorkspaceNetns{}, fmt.Errorf(
				"%w: workspace %s provisioning step %d/%d failed: %v",
				ErrWorkspaceIsolationUnavailable, netns.WorkspaceUUID, index+1, len(netns.Commands), err)
		}
	}
	return netns, nil
}

// ProvisionAll creates the namespace of every live workspace in netns mode. It
// reports the namespaces it created and the per-workspace refusal of the ones it
// did not, never aborting the batch: one sensitive workspace the host cannot
// carry must not hide the outcome of the others.
func (p *NetnsProvisioner) ProvisionAll(ctx context.Context, status NetworkPlanStatus, host NetnsHostSupport, now time.Time) ([]WorkspaceNetns, []string) {
	var netnsList []WorkspaceNetns
	var errs []string
	for _, plan := range status.Workspaces {
		netns, err := p.Provision(ctx, plan, host, now)
		if err != nil {
			errs = append(errs, fmt.Sprintf("workspace %s: %v", plan.WorkspaceUUID, err))
			continue
		}
		netnsList = append(netnsList, netns)
	}
	return netnsList, errs
}

// Release drops the namespace of a released workspace through the same exec
// seam. It refuses without a runner rather than pretending the namespace is
// gone, so a stale namespace is never leaked silently.
func (p *NetnsProvisioner) Release(ctx context.Context, netns WorkspaceNetns) error {
	runner := p.attachedRunner()
	if runner == nil {
		return fmt.Errorf(
			"%w: no provisioning runner is attached to the pool, so the namespace %s is not dropped",
			ErrWorkspaceIsolationUnavailable, netns.Name)
	}
	name := strings.TrimSpace(netns.Name)
	if name == "" {
		return fmt.Errorf("%w: %q has no namespace name", ErrInvalidWorkspaceUUID, netns.WorkspaceUUID)
	}
	if err := runner(ctx, netns, "ip netns del "+name); err != nil {
		return fmt.Errorf("%w: workspace %s namespace %s delete failed: %v", ErrWorkspaceIsolationUnavailable, netns.WorkspaceUUID, name, err)
	}
	return nil
}
