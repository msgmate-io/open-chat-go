package metrics

import (
	"backend/server/util"
	"encoding/json"
	"net/http"
)

type Metrics struct {
	NodeVersion string `json:"node_version"`
}

func (h *MetricsHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	metrics := Metrics{
		NodeVersion: VERSION,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}
