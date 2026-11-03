package admin

import (
	"backend/sandboxpool"
	"encoding/json"
	"net/http"
	"time"
)

// This file surfaces the resolved sandbox-pool network plan on the admin
// surface, next to the provider admission and the ci#613 pool egress views
// (follow-up of the ci#546 shared-sandbox networking design, tracked in ci#792).
// An operator otherwise has to read the pod environment to answer "which port
// block does this workspace own, is the pool exhausted, which lever moved the
// mode" - and a workspace whose dev server is unreachable looks like a plain
// timeout. The view is read-only and carries no credential material: ports,
// loopback aliases, env lever names/values and notes only.

// sandboxPoolNetworkResponse is the operator view of the pool network plan.
type sandboxPoolNetworkResponse struct {
	GeneratedAt string `json:"generated_at"`
	// Plan is the resolved policy/allocator state: mode, port block geometry,
	// loopback aliases, the live per-workspace blocks and the exhaustion state.
	Plan sandboxpool.NetworkPlanStatus `json:"plan"`
	// Network is the composed pool network surface next to the plan (ci#792
	// items 1/4/5): the transport declaration that publishes the per-workspace
	// port block, the fail-closed netns host check plus the per-workspace
	// namespaces of netns mode, and the distinct full-pool exhaustion signal.
	// It answers "is the block the workspace owns actually reachable, and what
	// would the lease path do when the pool fills" without reading the pod env.
	// Read-only and credential-free (ports, aliases, namespace names, levers).
	Network sandboxpool.PoolNetworkView `json:"network"`
	// Cgroup is the resolved per-workspace cgroup guard (ci#792 item 3): the
	// guard policy, the fail-closed host capability check and the resolved
	// slice of every live workspace, so the resource bound and any refusal
	// reason are visible next to the network plan. Read-only and credential-free.
	Cgroup sandboxpool.WorkspaceCgroupStatus `json:"cgroup"`
}

// @doc:sandbox-pool-network-status
// GetSandboxPoolNetworkStatus reports the resolved pool network plan (ci#792,
// follow-up of the ci#546 shared-sandbox networking design): the isolation mode,
// the port block geometry (base+size*slots), whether per-workspace loopback
// aliases are on, the block/alias each live workspace currently owns, the
// workspace ceiling and whether the pool has run out of blocks. It also carries
// the composed pool network surface (ci#792 items 1/4/5) - the transport
// declaration that publishes the per-workspace port block plus its reachability
// probe, the fail-closed netns host check and the per-workspace namespaces of
// netns mode, and the distinct full-pool exhaustion signal a lease can spill on
// - and the resolved per-workspace cgroup v2 guard of the bounded shared mode
// (ci#792 item 3): the CPU/memory/disk budget of each live workspace, the
// fail-closed host capability check and the refusal reason of a workspace the
// pool would not admit unbounded - a resource-bound refusal, which is a
// different condition from the port-block exhaustion above. It is read-only (it
// never allocates or releases a block, never creates a namespace or a cgroup
// slice), admin only, and never credential material.
func GetSandboxPoolNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	now := time.Now()
	// One plan resolution feeds both the plan and the composed network surface,
	// so the transport/exhaustion block can never describe a different plan than
	// the one served next to it.
	plan := sandboxpool.ResolveNetworkPlanStatus(now)
	payload := sandboxPoolNetworkResponse{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Plan:        plan,
		Network:     sandboxpool.PoolNetworkViewFromStatus(plan, now),
		Cgroup:      sandboxpool.ResolveWorkspaceCgroupStatus(now),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
