package metrics

import (
	"net/http"
)

// Liveness is the *public*, unauthenticated liveness probe of the backend.
//
// ci#739: the native-coder crash watchdog and the ci2 profile's `health_http`
// probe poll https://<host>/api/v1/metrics/health every five minutes
// (backend/nativecoder/recoverydeployment.go:CI2HealthPath), but no such route
// existed: `/api/v1/` is mounted behind AuthMiddleware, so every unauthenticated
// poll of the undefined path answered 403 — indistinguishable from a real crash
// to a "not 200" check. A healthy host therefore looked dead and the restricted
// recovery agent was dispatched with trigger=monitoring_crash (up to 288 needless
// runs/day).
//
// This handler is deliberately trivial: it must stay reachable even when the
// database, the provider or the integrations are unhealthy, otherwise a liveness
// probe turns a partial outage into a self-inflicted restart loop. It reports
// nothing about the caller and touches no dependency.
func (h *MetricsHandler) Liveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","version":"` + VERSION + `"}`))
}
