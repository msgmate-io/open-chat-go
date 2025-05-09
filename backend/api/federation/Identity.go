package federation

import (
	"backend/database"
	"encoding/json"
	"net/http"
)

type IdentityResponse struct {
	ID                 string   `json:"id"`
	Addresses          []string `json:"addresses"`
	ConnectMultiadress []string `json:"connect_multiadress"`
}

// Get own identity
//
//	@Summary      Get own identity
//	@Description  Get the identity of the current node
//	@Tags         federation
//	@Produce      json
//	@Success      200 {string} string "Node identity"
//	@Router       /api/v1/federation/identity [get]
func (h *FederationHandler) Identity(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	response := h.GetIdentity()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *FederationHandler) GetIdentity() IdentityResponse {
	var addresses []string
	var connectAddresses []string

	for _, addr := range h.Host.Addrs() {
		addresses = append(addresses, addr.String())
		connectAddr := addr.String() + "/p2p/" + h.Host.ID().String()
		connectAddresses = append(connectAddresses, connectAddr)
	}

	return IdentityResponse{
		ID:                 h.Host.ID().String(),
		Addresses:          addresses,
		ConnectMultiadress: connectAddresses,
	}
}
