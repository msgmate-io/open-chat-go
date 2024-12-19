package federation

import (
	"backend/server/util"
	"encoding/json"
	"net/http"
)

func (h *FederationHandler) WhitelistedPeers(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	json.NewEncoder(w).Encode(h.Gater.allowedPeers)
}
