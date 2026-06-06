package admin

import (
	"backend/sandboxpool"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// This file exposes the pool-level DNS/egress readiness gate of ci#613 (the
// follow-up of the ci#546 shared-sandbox networking design) on the admin
// surface. The probe itself has to run inside the pool pod, so the pod (or the
// pool start path) feeds its JSON report back here; without that ingestion
// surface a pool that cannot resolve cluster DNS or reach GitHub would only
// ever show up as a mysterious per-agent timeout.

// sandboxPoolEgressResponse is the operator view of the pool egress gate: the
// deployment levers, the probed endpoints and the last admitted verdict.
type sandboxPoolEgressResponse struct {
	GeneratedAt string `json:"generated_at"`
	// The Env* fields mirror the raw deployment levers so an operator can see
	// which knob moved the gate without reading the pod environment.
	EnvTargets         string `json:"env_targets"`
	EnvStrict          string `json:"env_strict"`
	EnvDisabled        string `json:"env_disabled"`
	EnvProbeLocally    string `json:"env_probe_locally"`
	EnvProbeTimeoutMS  string `json:"env_probe_timeout_ms"`
	EnvReportTTLMS     string `json:"env_report_ttl_ms"`
	EnvRecycleAfterBad string `json:"env_recycle_after_failures"`
	// ReportScript is the in-pod probe the admitted report comes from.
	ReportScript string                 `json:"report_script"`
	Status       sandboxpool.GateStatus `json:"status"`
}

// @doc:sandbox-pool-egress-status
// GetSandboxPoolEgressStatus reports whether a pooled backing sandbox may
// accept leases (ci#613): cluster DNS plus the GitHub/API endpoints the
// credential flow (ci#497) and `gh` need, the last admitted probe verdict and
// the remediation of its failure. Admin only, and never credential material.
func GetSandboxPoolEgressStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	// force: an operator asking for the gate state usually wants a fresh probe.
	// It only has an effect when the backend shares the pool's pod network
	// (EnvProbeLocally); otherwise this stays a pure read of the last report.
	force := strings.TrimSpace(r.URL.Query().Get("force")) != ""
	gate := sandboxpool.DefaultGate()

	payload := sandboxPoolEgressResponse{
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
		EnvTargets:         os.Getenv(sandboxpool.EnvTargets),
		EnvStrict:          os.Getenv(sandboxpool.EnvStrict),
		EnvDisabled:        os.Getenv(sandboxpool.EnvDisabled),
		EnvProbeLocally:    os.Getenv(sandboxpool.EnvProbeLocally),
		EnvProbeTimeoutMS:  os.Getenv(sandboxpool.EnvProbeTimeoutMS),
		EnvReportTTLMS:     os.Getenv(sandboxpool.EnvReportTTLMS),
		EnvRecycleAfterBad: os.Getenv(sandboxpool.EnvRecycleAfterFails),
		ReportScript:       sandboxpool.ProbeScriptPath,
		Status:             gate.Status(force),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// @doc:sandbox-pool-egress-report
// IngestSandboxPoolEgressReport admits the JSON report the in-pod probe printed
// (ci#613). A failing report blocks new leases and, after the configured streak,
// requests the pool's recycling; the verdict is returned so the pool start path
// can gate on it and log why. Admin only, and the body is validated so a pool
// pod cannot smuggle credential material into the backend.
func IngestSandboxPoolEgressReport(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Unable to read probe report", http.StatusBadRequest)
		return
	}

	report, err := sandboxpool.ParseReport(body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sandboxpool.ErrPoolEgressInvalidReport) {
			status = http.StatusUnprocessableEntity
		}
		http.Error(w, err.Error(), status)
		return
	}

	gate := sandboxpool.DefaultGate()
	admitErr := gate.Admit(report)

	payload := map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"pool":         report.Pool,
		"healthy":      report.Healthy(),
		"admitted":     admitErr == nil,
		"summary":      report.Summary(),
		"remediation":  report.Remediation(),
		"status":       gate.Status(false),
	}
	if admitErr != nil {
		payload["error"] = admitErr.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	// A rejected pool is not a client error: the report was well-formed and the
	// pool is simply unhealthy, so the verdict travels in the body.
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}
