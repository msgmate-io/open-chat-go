package admin

import (
	"backend/runtimecfg"
	"backend/server/util"
	"encoding/json"
	"net/http"
)

// @doc:open-chat-server-runtime-config
// Admin-only endpoint that exposes effective runtime values used by the
// current `open-chat server` process (flags/env-derived), including sensitivity
// markers so frontend docs can provide masked/unmasked rendering.
func GetServerRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"values": runtimecfg.GetAll(),
	})
}
