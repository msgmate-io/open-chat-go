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
)

type NodeInfo struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

// TODO: allow directly assigning the node to a network in the future
type RegisterNode struct {
	Name                string   `json:"name"`
	Addresses           []string `json:"addresses"`
	RequestRegistration bool     `json:"request_registration"`
	AddToNetwork        string   `json:"add_to_network"`
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

	node, err := RegisterNodeRaw(DB, h, data)
	if err != nil {
		log.Println("Error registering node: ", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

func RegisterNodeRaw(DB *gorm.DB, h *FederationHandler, data RegisterNode) (database.Node, error) {
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
	q := DB.Where("peer_id = ?", info.ID.String()).First(&existingNode)
	if q.Error == nil {
		return database.Node{}, fmt.Errorf("Peer ID already registered")
	}
	peerInfo := h.Host.Peerstore().PeerInfo(info.ID)

	if peerInfo.ID == "" {
		h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	}

	node := database.Node{
		NodeName:  data.Name,
		PeerID:    info.ID.String(),
		Addresses: nodeAddresses,
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
		networkMembership := database.NetworkMember{
			NetworkID: network.ID,
			NodeID:    node.ID,
			Status:    "accepted",
		}
		q = DB.Create(&networkMembership)
		if q.Error != nil {
			return database.Node{}, fmt.Errorf("Internal server error")
		}
	}

	if data.AddToNetwork != "" {
		// h.StartNetworkSyncProcess(DB, data.AddToNetwork)
	} else {
		// in non networking mode we instate an auto-ping to the node
		ownPeerId := h.Host.ID()
		SendNodeRequest(DB, h, node.UUID, RequestNode{
			Method:  "GET",
			Path:    "/api/v1/federation/nodes/" + ownPeerId.String() + "/ping",
			Headers: map[string]string{},
			Body:    "",
		})

		// start node auto ping
		// StartNodeAutoPing(DB, node.UUID, h, 60*time.Second)
	}

	// TODO: allow automaticly requestion registration at that node
	if data.RequestRegistration {
		log.Println("Requesting registration at node")
		identity := h.GetIdentity()
		var registerNodeRequest RegisterNode
		registerNodeRequest.Name = data.Name
		registerNodeRequest.Addresses = identity.ConnectMultiadress
		registerNodeRequest.RequestRegistration = false

		registerNodeRequestJson, err := json.Marshal(registerNodeRequest)
		if err != nil {
			return database.Node{}, fmt.Errorf("Internal server error")
		}

		SendNodeRequest(DB, h, node.UUID, RequestNode{
			Method:  "POST",
			Path:    "/api/v1/federation/nodes/register/request",
			Headers: map[string]string{},
			Body:    string(registerNodeRequestJson),
		})
	}

	return node, nil
}

// RequestNodeRegistration requests a node registration from a node
//
//	@Summary      Request node registration
//	@Description  Request node registration
//	@Tags         federation
//	@Accept       json
//	@Produce      json
//	@Router       /api/v1/federation/nodes/register/request [post]
func (h *FederationHandler) RequestNodeRegistration(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		return
	}

	var data RegisterNode
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var nodeAddresses []string
	var info *peer.AddrInfo
	var prevPeerId string = ""
	for _, address := range data.Addresses {
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
		nodeAddresses = append(nodeAddresses, address)
		if prevPeerId != "" && prevPeerId != info.ID.String() {
			http.Error(w, "Trying to register node with multiple peer ids", http.StatusBadRequest)
			return
		}
		prevPeerId = info.ID.String()
	}

	contactRequest := database.ContactRequest{
		NodeName:  data.Name,
		Addresses: nodeAddresses,
		Status:    "pending",
	}
	prettyContactRequest, err := json.MarshalIndent(contactRequest, "", "  ")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	log.Println("Contact Request: ", string(prettyContactRequest))
	DB.Create(&contactRequest)

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
