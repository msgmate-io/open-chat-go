package federation

import (
	"backend/database"
	"backend/server/util"
	"context"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"time"
)

func (h *FederationHandler) Ping(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	peerId := r.PathValue("peer_id")
	if peerId == "" {
		http.Error(w, "Invalid peer ID", http.StatusBadRequest)
		return
	}

	// retrive the node by the peer id
	var node database.Node
	if err := DB.Model(&database.Node{}).Where("peer_id = ?", peerId).First(&node).Error; err != nil {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	// create a new ping object
	ping := database.Ping{
		NodeID:   node.ID,
		PingedAt: time.Now(),
	}

	if err := DB.Create(&ping).Error; err != nil {
		http.Error(w, "Unable to create ping", http.StatusInternalServerError)
		return
	}

	// update the node with the new ping
	if err := DB.Model(&database.Node{}).Where("peer_id = ?", peerId).Update("latest_ping_id", ping.ID).Error; err != nil {
		http.Error(w, "Unable to update node", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Pong"))
}

func StartNodeAutoPing(DB *gorm.DB, nodeUUID string, h *FederationHandler, sleepTime time.Duration) (error, context.CancelFunc) {
	fmt.Println("Starting node auto ping for node", nodeUUID)
	ownPeerId := h.Host.ID().String()
	var node database.Node
	q := DB.Preload("Addresses").Where("uuid = ?", nodeUUID).First(&node)

	if q.Error != nil {
		return q.Error, nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		ticker := time.NewTicker(sleepTime)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fmt.Println("Sending Auto-Ping to node", nodeUUID)
				SendRequestToNode(h, node, RequestNode{
					Method: "POST",
					Path:   "/api/v1/federation/nodes/" + ownPeerId + "/ping",
				})
			}
		}
	}()

	return nil, cancel
}
