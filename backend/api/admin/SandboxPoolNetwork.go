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
}

// @doc:sandbox-pool-network-status
// GetSandboxPoolNetworkStatus reports the resolved pool network plan (ci#792,
// follow-up of the ci#546 shared-sandbox networking design): the isolation mode,
// the port block geometry (base+size*slots), whether per-workspace loopback
// aliases are on, the block/alias each live workspace currently owns, the
// workspace ceiling and whether the pool has run out of blocks. It is read-only
// (it never allocates or releases a block), admin only, and never credential
// material.
func GetSandboxPoolNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	payload := sandboxPoolNetworkResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Plan:        sandboxpool.ResolveNetworkPlanStatus(time.Now()),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
