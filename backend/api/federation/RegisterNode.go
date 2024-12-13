package federation

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"log"
	"net/http"
)

type RegisterNode struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

// Registers a Peer to Peer Node
//
//	@Summary      Register a Peer to Peer Node
//	@Description  Register a Peer to Peer Node
//	@Tags         federation
//	@Accept       json
//	@Produce      json
//	@Param        node body RegisterNode true "Node to register"
//	@Success      200 {string} string "Node registered"
//	@Failure      400 {string} string "Invalid JSON"
//	@Failure      403 {string} string "User is not an admin"
//	@Router       /api/v1/federation/nodes/register [post]
func (h *FederationHandler) RegisterNode(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var data RegisterNode
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var nodeAddresses []database.NodeAddress

	for _, address := range data.Addresses {
		log.Println("Register Address: ", address)
		nodeAddresses = append(nodeAddresses, database.NodeAddress{
			Address: address,
		})
	}

	// TODO: query all existing node adresses to make sure a NodeAdress is NEVER registered to multiple nodes
	// If a NodeAddress is registered twice this should almost always mean that the host nodes-peer-id has changed!

	var node = database.Node{
		NodeName:  data.Name,
		Addresses: nodeAddresses,
	}

	q := DB.Create(&node)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}
