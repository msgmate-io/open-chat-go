package federation

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
)

type ListedNode struct {
	UUID      string                 `json:"uuid"`
	NodeName  string                 `json:"node_name"`
	Addresses []database.NodeAddress `json:"addresses"`
}

type PaginatedNodes struct {
	database.Pagination
	Rows []ListedNode
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
		listedNodes[i] = ListedNode{
			UUID:      node.UUID,
			NodeName:  node.NodeName,
			Addresses: node.Addresses,
		}
	}

	pagination.Rows = listedNodes

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
