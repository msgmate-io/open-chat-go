package federation

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"log"
	"net/http"
	"time"
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

	var info *peer.AddrInfo
	var prevPeerId string = ""
	for _, address := range data.Addresses {
		log.Println("Register Address: ", address)
		maddr, err := multiaddr.NewMultiaddr(address)
		if err != nil {
			http.Error(w, "Invalid address", http.StatusBadRequest)
			return
		}
		info, err = peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			http.Error(w, "Invalid address", http.StatusBadRequest)
			return
		}
		nodeAddresses = append(nodeAddresses, database.NodeAddress{
			Address: address,
		})
		if prevPeerId != "" && prevPeerId != info.ID.String() {
			http.Error(w, "Trying to register node with multiple peer ids", http.StatusBadRequest)
			return
		}
		prevPeerId = info.ID.String()

	}

	if info.ID.String() == h.Host.ID().String() {
		http.Error(w, "Peer ID matches own identity", http.StatusBadRequest)
		return
	}

	var existingNode database.Node
	q := DB.Where("peer_id = ?", info.ID.String()).First(&existingNode)
	if q.Error == nil {
		http.Error(w, "Peer ID already registered", http.StatusBadRequest)
		return
	}
	peerInfo := h.Host.Peerstore().PeerInfo(info.ID)

	if peerInfo.ID == "" {
		h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
		log.Println("Peer not present in peerstore")
	} else {
		log.Println("Peer already present in peerstore")
	}

	node := database.Node{
		NodeName:  data.Name,
		PeerID:    info.ID.String(),
		Addresses: nodeAddresses,
	}

	q = DB.Create(&node)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.Gater.AddAllowedPeer(info.ID)

	// directly try to 'ping' the node once!
	ownPeerId := h.Host.ID()
	SendNodeRequest(DB, h, node.UUID, RequestNode{
		Method:  "GET",
		Path:    "/api/v1/federation/nodes/" + ownPeerId.String() + "/ping",
		Headers: map[string]string{},
		Body:    "",
	})

	// start node auto ping
	StartNodeAutoPing(DB, node.UUID, h, 60*time.Second)

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}
