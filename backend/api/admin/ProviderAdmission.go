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
// controller: the deployment levers, the resolved default and the per-key
// in-flight/queue state. It carries no key material.
type providerAdmissionResponse struct {
	GeneratedAt string `json:"generated_at"`
	// EnvMaxConcurrency is the raw value of the deployment lever.
	EnvMaxConcurrency string `json:"env_max_concurrency"`
	// EnvQueueTimeout is the raw value of the deployment queue timeout lever.
	EnvQueueTimeout string `json:"env_queue_timeout_ms"`
	// DefaultMaxConcurrency is the cap used when nothing else is configured.
	DefaultMaxConcurrency int `json:"default_max_concurrency"`
	// Effective resolves the deployment-wide policy (no chat config): the value
	// that takes effect for a chat without a per-agent override.
	Effective msgmate.ProviderAdmissionConfig `json:"effective"`
	// Limiter is the state of every admission key that has been used since the
	// process started (effective cap, in-flight count, queue depth).
	Limiter []msgmate.ProviderAdmissionSnapshot `json:"limiters"`
}

// @doc:provider-admission-status
// GetProviderAdmissionStatus reports the effective provider in-flight cap and
// the current in-flight/queue counts per provider key (ci#620). Admin only: it
// exposes fleet-wide provider pressure, never key material.
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

	payload := providerAdmissionResponse{
		GeneratedAt:           time.Now().UTC().Format(time.RFC3339),
		EnvMaxConcurrency:     os.Getenv(msgmate.ProviderMaxConcurrencyEnvKey),
		EnvQueueTimeout:       os.Getenv(msgmate.ProviderQueueTimeoutEnvKey),
		DefaultMaxConcurrency: msgmate.DefaultProviderMaxConcurrency,
		Effective:             msgmate.ResolveProviderAdmissionConfig(nil),
		Limiter:               msgmate.ProviderAdmissionStatus(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
