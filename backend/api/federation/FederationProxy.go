package federation

import (
	"backend/database"
	"net/http"
)

/**
* e.g.: POST /api/federation/{node_uuid}/api/user/self
* Proxies the request to the 'node' at `/api/user/self`
 */
func (h *FederationHandler) FederationProxy(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	nodeUuid := r.PathValue("node_uuid") // TODO - validate chat UUID!
	if nodeUuid == "" {
		http.Error(w, "Invalid node UUID", http.StatusBadRequest)
		return
	}

	// 1 - retrieve information about the 'node'
	var node database.Node
	q := database.DB.Where("uuid = ?", nodeUuid).First(&node)

	if q.Error != nil {
		http.Error(w, "Cound't find node with that uuid", http.StatusNotFound)
	}
}
