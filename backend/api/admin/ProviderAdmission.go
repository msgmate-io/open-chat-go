package admin

import (
	"backend/api/msgmate"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"os"
	"time"
)

// This file exposes the effective native-coder provider admission state
// (ci#620). The configured provider concurrency used to be pinned at the
// literal default of 3; an operator therefore needs one place that answers
// "what cap is actually in force right now, and how many requests are in
// flight?" without reading the backend log.

// providerAdmissionResponse is the operator view of the provider admission
// controller: the deployment levers, the resolved policy and the per-key
// in-flight/queue state. It carries no key material.
type providerAdmissionResponse struct {
	GeneratedAt string `json:"generated_at"`
	// EnvMaxConcurrency is the raw value of the deployment lever. An empty value
	// means the lever is unset and the per-agent key/default applies.
	EnvMaxConcurrency string `json:"env_max_concurrency"`
	// EnvQueueTimeoutMS is the raw value of the deployment queue-timeout lever.
	EnvQueueTimeoutMS string `json:"env_queue_timeout_ms"`
	// DefaultMaxConcurrency is the cap used when nothing else is configured.
	DefaultMaxConcurrency int `json:"default_max_concurrency"`
	// EffectiveMaxConcurrency is the cap that actually takes effect for a chat
	// without a per-agent override, and EffectiveSource names the config value
	// that won (env:/agent:/pool:/default).
	EffectiveMaxConcurrency int    `json:"effective_max_concurrency"`
	EffectiveSource         string `json:"effective_source"`
	// EffectiveLimitingSource is the single bound that decided
	// EffectiveMaxConcurrency (`env:KEY`, `agent:KEY`, `pool:KEY` or "default"),
	// so an operator can see at a glance which lever actually has to move when a
	// request comes back saturated (ci#620 follow-up).
	EffectiveLimitingSource string `json:"effective_limiting_source"`
	EffectiveQueueTimeoutMS int64  `json:"effective_queue_timeout_ms"`
	// Limiters is the state of every admission key used since process start
	// (effective cap, in-flight count, queue depth, counters).
	Limiters []msgmate.ProviderAdmissionSnapshot `json:"limiters"`
	// SandboxMemoryBudget is the memory side of the same sizing decision
	// (ci#662): the per-sandbox request/limit, the node's allocatable memory,
	// how many sandboxes fit and the resulting agents-per-pool bound. A byte
	// field of 0 means "not measured yet" and no bound is applied.
	SandboxMemoryBudget msgmate.SandboxMemoryBudget `json:"sandbox_memory_budget"`
}

// @doc:provider-admission-status
// GetProviderAdmissionStatus reports the effective provider in-flight cap and
// the current in-flight/queue counts per provider key (ci#620). Admin only: it
// exposes fleet-wide provider pressure and never key material.
func GetProviderAdmissionStatus(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	effective := msgmate.ResolveProviderAdmissionConfig(nil)
	payload := providerAdmissionResponse{
		GeneratedAt:             time.Now().UTC().Format(time.RFC3339),
		EnvMaxConcurrency:       os.Getenv(msgmate.ProviderMaxConcurrencyEnvKey),
		EnvQueueTimeoutMS:       os.Getenv(msgmate.ProviderQueueTimeoutEnvKey),
		DefaultMaxConcurrency:   msgmate.DefaultProviderMaxConcurrency,
		EffectiveMaxConcurrency: effective.MaxConcurrency,
		EffectiveSource:         effective.Source,
		EffectiveLimitingSource: effective.LimitingSource(),
		EffectiveQueueTimeoutMS: effective.QueueTimeout.Milliseconds(),
		Limiters:                msgmate.ProviderAdmissionStatus(),
		// ci#662: the memory budget is resolved without a per-agent config here
		// (the admin surface has no chat context), so it reports the
		// deployment-wide levers and the default reserve.
		SandboxMemoryBudget: msgmate.ResolveSandboxMemoryBudget(nil),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
