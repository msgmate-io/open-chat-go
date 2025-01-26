package federation

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type ListedNode struct {
	UUID               string                 `json:"uuid"`
	NodeName           string                 `json:"node_name"`
	PeerID             string                 `json:"peer_id"`
	NetworkMemberships []SimpleNetworkMember  `json:"network_memberships"`
	Addresses          []database.NodeAddress `json:"addresses"`
	LatestContact      time.Time              `json:"latest_contact"`
}

type PaginatedNodes struct {
	database.Pagination
	Rows []ListedNode `json:"rows"`
}

type SimpleNetworkMember struct {
	NetworkName string    `json:"network_name"`
	LastSync    time.Time `json:"last_sync"`
}

// List Nodes
// @Summary      Get federation nodes
// @Description  Retrieve a list of federation nodes
// @Tags         federation
// @Accept       json
// @Produce      json
// @Param        page  query  int  false  "Page number"  default(1)
// @Param        limit query  int  false  "Page size"     default(10)
// @Success      200 {array}  federation.PaginatedNodes "List of nodes"
// @Failure      400 {string} string "Invalid request"
// @Failure      403 {string} string "User is not an admin"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/federation/nodes/list [get]
func (h *FederationHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 10}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	var nodes []database.Node
	q := DB.Scopes(database.Paginate(&nodes, &pagination, DB)).
		Preload("Addresses").
		Find(&nodes)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(nodes) == 0 && pagination.Page > 1 {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}

	listedNodes := make([]ListedNode, len(nodes))
	for i, node := range nodes {
		var networkMemberShips []database.NetworkMember
		var simpleNetworkMemberShips []SimpleNetworkMember
		var latestContact time.Time
		DB.Model(&database.NetworkMember{}).Preload("Network").Where("node_id = ?", node.ID).Find(&networkMemberShips)
		for _, networkMemberShip := range networkMemberShips {
			simpleNetworkMemberShips = append(simpleNetworkMemberShips, SimpleNetworkMember{
				NetworkName: networkMemberShip.Network.NetworkName,
				LastSync:    networkMemberShip.LastSync,
			})
			if networkMemberShip.LastSync.After(latestContact) {
				latestContact = networkMemberShip.LastSync
			}
		}
		listedNodes[i] = ListedNode{
			UUID:               node.UUID,
			NodeName:           node.NodeName,
			Addresses:          node.Addresses,
			PeerID:             node.PeerID,
			NetworkMemberships: simpleNetworkMemberShips,
			LatestContact:      latestContact,
		}
	}

	pagination.Rows = listedNodes

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
