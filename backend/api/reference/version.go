package reference

import (
	"backend/api/metrics"
	"encoding/json"
	"net/http"
)

type VersionResponse struct {
	Version string `json:"version"`
}

func VersionHandler(w http.ResponseWriter, r *http.Request) {
	version := VersionResponse{
		Version: metrics.VERSION,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(version)
}
