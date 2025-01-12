package federation

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
	"log"
	"net/http"
	"time"
)

type NodeInfo struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

type NodeRepresentation struct {
	Name      string   `json:"name"`
	PeerId    string   `json:"peer_id"`
	Addresses []string `json:"addresses"`
}

type NodeSyncInfo struct {
	Name        string    `json:"name"`
	PeerId      string    `json:"peer_id"`
	Addresses   []string  `json:"addresses"`
	LastUpdated time.Time `json:"last_updated"`
}

// TODO: allow directly assigning the node to a network in the future
type RegisterNode struct {
	Name         string     `json:"name"`
	Addresses    []string   `json:"addresses"`
	AddToNetwork string     `json:"add_to_network"`
	LastChanged  *time.Time `json:"last_changed"`
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

	var data RegisterNode
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// you have to either be admin OR the network owner
	// TODO the ownership check shoul be improved!
	// TODO: also it must somehow be strictly enforced that network names cannot be registed by users!!!
	if !user.IsAdmin && (data.AddToNetwork != "" && h.Networks[data.AddToNetwork].NetworkName != user.Email) {
		http.Error(w, "User is not an admin or the network owner", http.StatusForbidden)
		return
	}

	node, err := RegisterNodeRaw(DB, h, data, data.LastChanged)
	if err != nil {
		log.Println("Error registering node: ", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

func RegisterNodeRaw(DB *gorm.DB, h *FederationHandler, data RegisterNode, lastChanged *time.Time) (database.Node, error) {
	var nodeAddresses []database.NodeAddress

	var info *peer.AddrInfo
	var prevPeerId string = ""
	for _, address := range data.Addresses {
		log.Println("Register Address: ", address)
		maddr, err := multiaddr.NewMultiaddr(address)
		if err != nil {
			return database.Node{}, fmt.Errorf("Invalid address")
		}
		info, err = peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return database.Node{}, fmt.Errorf("Invalid address")
		}
		nodeAddresses = append(nodeAddresses, database.NodeAddress{
			Address: address,
		})
		if prevPeerId != "" && prevPeerId != info.ID.String() {
			return database.Node{}, fmt.Errorf("Trying to register node with multiple peer ids")
		}
		prevPeerId = info.ID.String()

	}

	var existingNode database.Node
	q := DB.Where("peer_id = ?", info.ID.String()).Preload("Addresses").First(&existingNode)
	if q.Error == nil {
		// we still need to assure that the network member ship exists already!
		if data.AddToNetwork != "" {
			networkMemberShip := database.NetworkMember{}
			q = DB.Where("node_id = ?", existingNode.ID).Where("network_id = ?", h.Networks[data.AddToNetwork].ID).First(&networkMemberShip)
			if q.Error != nil {
				// then we need to still create that network membership
				network, ok := h.Networks[data.AddToNetwork]
				if !ok {
					return database.Node{}, fmt.Errorf("Network not found")
				}
				h.AddNetworkPeerId(data.AddToNetwork, existingNode.PeerID)
				// give pass time so that node has to sync immediately
				timeBefore5Minutes := time.Now().Add(-5 * time.Minute)
				networkMembership := database.NetworkMember{
					NetworkID: network.ID,
					NodeID:    existingNode.ID,
					LastSync:  timeBefore5Minutes,
					Status:    "accepted",
				}
				q = DB.Create(&networkMembership)
				if q.Error != nil {
					return database.Node{}, fmt.Errorf("Internal server error")
				}
			}
		}
		return existingNode, fmt.Errorf("Peer ID already registered")
	}
	peerInfo := h.Host.Peerstore().PeerInfo(info.ID)

	if peerInfo.ID == "" {
		h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	}
	if lastChanged == nil {
		now := time.Now()
		lastChanged = &now
	}

	node := database.Node{
		NodeName:    data.Name,
		PeerID:      info.ID.String(),
		Addresses:   nodeAddresses,
		LastChanged: *lastChanged,
	}

	q = DB.Create(&node)

	if q.Error != nil {
		return database.Node{}, fmt.Errorf("Internal server error")
	}

	h.Gater.AddAllowedPeer(info.ID)

	if data.AddToNetwork != "" {
		network, ok := h.Networks[data.AddToNetwork]
		if !ok {
			return database.Node{}, fmt.Errorf("Network not found")
		}
		h.AddNetworkPeerId(data.AddToNetwork, node.PeerID)
		// give pass time so that node has to sync immediately
		timeBefore5Minutes := time.Now().Add(-5 * time.Minute)
		networkMembership := database.NetworkMember{
			NetworkID: network.ID,
			NodeID:    node.ID,
			LastSync:  timeBefore5Minutes,
			Status:    "accepted",
		}
		q = DB.Create(&networkMembership)
		if q.Error != nil {
			return database.Node{}, fmt.Errorf("Internal server error")
		}
	}

	return node, nil
}
