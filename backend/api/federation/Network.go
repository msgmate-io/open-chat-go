package federation

import (
	"backend/api/user"
	"backend/database"
	"backend/server/util"
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"log"
	"net/http"
	"regexp"
	"sort"
	"time"
)

type NetworkCreate struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func orderAdressesAlphabetically(addresses []string) []string {
	sort.Strings(addresses)
	return addresses
}

func (h *FederationHandler) NetworkCreate(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var data NetworkCreate
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	err = h.NetworkCreateRAW(DB, data.Name, data.Password)
	if err != nil {
		http.Error(w, "Network already exists", http.StatusBadRequest)
		return
	}

	h.StartNetworkSyncProcess(DB, data.Name)

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("Network created"))
}

func (h *FederationHandler) NetworkCreateRAW(DB *gorm.DB, networkName string, networkPassword string) error {
	// first check if the network already exists
	var network database.Network
	DB.Where("network_name = ?", networkName).First(&network)
	if network.ID != 0 {
		return nil
	}
	network = database.Network{
		NetworkName:     networkName,
		NetworkPassword: networkPassword,
	}
	DB.Create(&network)
	util.CreateUser(DB, networkName, networkPassword, false)
	h.Networks[networkName] = network
	h.NetworkPeerIds[networkName] = map[string]bool{}
	return nil
}

type SyncGet struct {
	PeerIds        []string          `json:"peer_ids"`
	RequestorInfo  NodeSyncInfo      `json:"requestor_info"`
	PeerInfoHashes map[string]string `json:"peer_info_hashes"`
}

type SyncGetResponse struct {
	PeerIds        []string       `json:"peer_ids"`
	MissingNodes   []RegisterNode `json:"missing_nodes"`
	DifferentNodes []NodeSyncInfo `json:"different_nodes"`
}

func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (h *FederationHandler) SyncGet(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	var data SyncGet
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	networkName := r.PathValue("network_name")
	if networkName == "" {
		http.Error(w, "Invalid network name", http.StatusBadRequest)
		return
	}

	// you have to either be admin OR the network owner
	// TODO the ownership check shoul be improved!
	// TODO: also it must somehow be strictly enforced that network names cannot be registed by users!!!
	if !user.IsAdmin && (networkName != "" && h.Networks[networkName].NetworkName != user.Email) {
		http.Error(w, "User is not an admin or the network owner", http.StatusForbidden)
		return
	}

	var network database.Network
	DB.Where("network_name = ?", networkName).First(&network)
	if network.ID == 0 {
		http.Error(w, "Network not found", http.StatusNotFound)
		return
	}

	var networkMembers []database.NetworkMember
	DB.Where("network_id = ?", network.ID).Preload("Node").Preload("Node.Addresses").Find(&networkMembers)
	var networkMemberPeerIds []string
	for _, networkMember := range networkMembers {
		networkMemberPeerIds = append(networkMemberPeerIds, networkMember.Node.PeerID)
	}

	var missingNodes []RegisterNode
	var differentNodes []NodeSyncInfo
	for _, networkMember := range networkMembers {
		if !Contains(data.PeerIds, networkMember.Node.PeerID) {
			addresses := make([]string, len(networkMember.Node.Addresses))
			for i, addr := range networkMember.Node.Addresses {
				addresses[i] = addr.Address
			}
			missingNodes = append(missingNodes, RegisterNode{
				Name:                networkMember.Node.NodeName,
				Addresses:           addresses,
				RequestRegistration: false,
				AddToNetwork:        networkName,
				LastChanged:         &networkMember.Node.LastChanged,
			})
		} else {
			// if it's contained we need to check if it's outdated
			// calculate & compare the node info hash
			addresses := make([]string, len(networkMember.Node.Addresses))
			for i, addr := range networkMember.Node.Addresses {
				addresses[i] = addr.Address
			}
			networkMemberInfoJson, err := json.Marshal(NodeRepresentation{
				Name:      networkMember.Node.NodeName,
				PeerId:    networkMember.Node.PeerID,
				Addresses: orderAdressesAlphabetically(addresses),
			})
			if err != nil {
				log.Println("Error marshalling network member info", err)
				continue
			}
			if networkMember.Node.PeerID != data.RequestorInfo.PeerId {
				if data.PeerInfoHashes[networkMember.Node.PeerID] != util.Hash(string(networkMemberInfoJson)) {
					// TODO: send the node info to the node
					addresses = make([]string, len(networkMember.Node.Addresses))
					for i, addr := range networkMember.Node.Addresses {
						addresses[i] = addr.Address
					}
					differentNodes = append(differentNodes, NodeSyncInfo{
						Name:        networkMember.Node.NodeName,
						PeerId:      networkMember.Node.PeerID,
						Addresses:   orderAdressesAlphabetically(addresses),
						LastUpdated: networkMember.Node.LastChanged,
					})
				}
			}
		}
	}

	var response SyncGetResponse
	response.PeerIds = networkMemberPeerIds
	response.MissingNodes = missingNodes
	response.DifferentNodes = differentNodes
	// Last we check if the 'RequestorInfo' is in our network
	// log.Println("Requestor info", data.RequestorInfo, networkMemberPeerIds)
	if !Contains(networkMemberPeerIds, data.RequestorInfo.PeerId) {
		// log.Println("Requestor info is in our network, adding it directly")
		// add directly to own nodes
		_, err := RegisterNodeRaw(DB, h, RegisterNode{
			Name:                data.RequestorInfo.Name,
			Addresses:           data.RequestorInfo.Addresses,
			RequestRegistration: false,
			AddToNetwork:        networkName,
			LastChanged:         &data.RequestorInfo.LastUpdated,
		}, &data.RequestorInfo.LastUpdated)
		if err != nil {
			log.Println("Error registering requestor node", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *FederationHandler) StartNetworkSyncProcess(DB *gorm.DB, networkName string) {
	// Create a context with cancel function
	ctx, cancel := context.WithCancel(context.Background())
	h.NetworkSyncs[networkName] = cancel

	// sync network once every 30 seconds
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				h.SyncNetwork(DB, networkName)
				time.Sleep(30 * time.Second)
			}
		}
	}()
}

func (h *FederationHandler) SyncNetwork(DB *gorm.DB, networkName string) {
	h.NetworkSyncBlocker[networkName] = true
	var network database.Network
	ownIdentity := h.GetIdentity()
	DB.Where("network_name = ?", networkName).First(&network)

	var networkMembers []database.NetworkMember
	var networkMembersToSync []database.NetworkMember
	// get all network members wher last_sync is older than 60 seconds
	DB.Where("network_id = ?", network.ID).Preload("Node").Preload("Node.Addresses").Find(&networkMembers)
	DB.Where("network_id = ? AND last_sync < ? AND node_id NOT IN (SELECT id FROM nodes WHERE peer_id = ?)",
		network.ID,
		time.Now().Add(-60*time.Second),
		h.Host.ID().String()).
		Preload("Node").
		Find(&networkMembersToSync)
	var networkMemberPeerIds []string
	var memberInfoHashes map[string]string = make(map[string]string)
	var networkMemberInfoMap map[string]NodeRepresentation = make(map[string]NodeRepresentation)
	var peerIdNodeMap map[string]database.Node = make(map[string]database.Node)

	for _, networkMember := range networkMembers {
		peerIdNodeMap[networkMember.Node.PeerID] = networkMember.Node
		networkMemberPeerIds = append(networkMemberPeerIds, networkMember.Node.PeerID)

		adresses := make([]string, len(networkMember.Node.Addresses))
		for i, addr := range networkMember.Node.Addresses {
			adresses[i] = addr.Address
		}
		networkMemberInfo := NodeRepresentation{
			Name:      networkMember.Node.NodeName,
			PeerId:    networkMember.Node.PeerID,
			Addresses: orderAdressesAlphabetically(adresses),
		}
		networkMemberInfoJson, err := json.Marshal(networkMemberInfo)
		if err != nil {
			log.Println("Error marshalling network member info", err)
			continue
		}
		networkMemberInfoMap[networkMember.Node.PeerID] = networkMemberInfo
		memberInfoHashes[networkMember.Node.PeerID] = util.Hash(string(networkMemberInfoJson))
	}

	networkUser := user.UserLogin{
		Email:    network.NetworkName,
		Password: network.NetworkPassword,
	}
	networkUserJson, err := json.Marshal(networkUser)
	if err != nil {
		log.Println("Error marshalling network user", err)
		return
	}

	var ownNode database.Node
	DB.Where("peer_id = ?", ownIdentity.ID).Preload("Addresses").First(&ownNode)

	fmt.Println(fmt.Sprintf("Starting sync for network: '%s' with %d nodes of which %d are required to be synced", networkName, len(networkMembers), len(networkMembersToSync)))

	// Now start looping trough all the nodes that need to be synced
	for _, networkMemberToSync := range networkMembersToSync {

		var networkMemberNode database.Node
		DB.Where("id = ?", networkMemberToSync.NodeID).Preload("Addresses").First(&networkMemberNode)

		if networkMemberNode.PeerID == h.Host.ID().String() {
			continue
		}

		resp, err := SendRequestToNode(h, networkMemberNode, RequestNode{
			Method: "POST",
			Path:   "/api/v1/federation/networks/login",
			Body:   string(networkUserJson),
		}, T1mNetworkJoinProtocolID)

		if err != nil {
			log.Println("Error sending request to node", err)
			continue
		}

		// if statuscode bussy = 429, means the node is currently syncing it's own network
		if resp.StatusCode == 429 {
			log.Println("Node is busy, skipping sync with node", networkMemberToSync.Node.PeerID)
			continue
		}

		// Otherwise we can parse the session id from that respons header
		cookieHeader := resp.Header.Get("Set-Cookie")
		re := regexp.MustCompile(`session_id=([^;]+)`)

		var sessionId string
		match := re.FindStringSubmatch(cookieHeader)
		if match != nil && len(match) > 1 {
			sessionId = match[1]
		} else {
			log.Println("No session id found in unable to authenticate with peer!", resp)
			continue
		}

		syncGetRequest := SyncGet{
			PeerIds: networkMemberPeerIds,
			RequestorInfo: NodeSyncInfo{
				Name:        ownNode.NodeName,
				PeerId:      ownIdentity.ID,
				Addresses:   ownIdentity.ConnectMultiadress,
				LastUpdated: ownNode.LastChanged,
			},
			PeerInfoHashes: memberInfoHashes,
		}
		syncGetRequestJson, err := json.Marshal(syncGetRequest)
		if err != nil {
			log.Println("Error marshalling sync get request", err)
			continue
		}
		resp, err = SendRequestToNode(h, networkMemberNode, RequestNode{
			Method: "GET",
			Path:   fmt.Sprintf("/api/v1/federation/networks/sync/%s/get", networkName),
			Headers: map[string]string{
				"Cookie": fmt.Sprintf("session_id=%s", sessionId),
			},
			Body: string(syncGetRequestJson),
		}, T1mNetworkJoinProtocolID)

		if err != nil {
			log.Println("Error sending request to node", err)
			continue
		}

		if resp.StatusCode == http.StatusBadRequest {
			log.Println("Bad request response from node:", resp, resp.Body)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			// update the LastSync field of the network member
			networkMemberToSync.LastSync = time.Now()
			DB.Save(&networkMemberToSync)
		}

		var syncNetworkResponse SyncGetResponse
		json.NewDecoder(resp.Body).Decode(&syncNetworkResponse)

		// first register all missing nodes
		if len(syncNetworkResponse.MissingNodes) != 0 {
			log.Println(fmt.Sprintf("Peer '%s' provided %d missing nodes", networkMemberNode.PeerID, len(syncNetworkResponse.MissingNodes)))
			prettyMissingNodes, err := json.MarshalIndent(syncNetworkResponse.MissingNodes, "", "  ")
			if err != nil {
				log.Println("Error marshalling missing nodes", err)
			}
			log.Println("Pretty missing nodes", string(prettyMissingNodes))
		}

		for _, missingNode := range syncNetworkResponse.MissingNodes {
			RegisterNodeRaw(DB, h, missingNode, missingNode.LastChanged)
		}

		if len(syncNetworkResponse.DifferentNodes) != 0 {
			log.Println(fmt.Sprintf("Peer '%s' provided %d different nodes", networkMemberNode.PeerID, len(syncNetworkResponse.DifferentNodes)))
			prettyDifferentNodes, err := json.MarshalIndent(syncNetworkResponse.DifferentNodes, "", "  ")
			if err != nil {
				log.Println("Error marshalling different nodes", err)
			}
			log.Println("Pretty different nodes", string(prettyDifferentNodes))
		}

		// check if nodes where the hashes differed needed updating
		for _, differentNode := range syncNetworkResponse.DifferentNodes {
			fmt.Println("Different node", differentNode.Name, differentNode.LastUpdated, "vs (my node)", peerIdNodeMap[differentNode.PeerId].LastChanged)
			if differentNode.PeerId == ownIdentity.ID {
				// nevery sync a change of the own node!
				continue
			}
			// Normalize timestamps to UTC before comparison
			ownLastChanged := peerIdNodeMap[differentNode.PeerId].LastChanged.UTC()
			differentNodeLastUpdated := differentNode.LastUpdated.UTC()

			fmt.Println("Own last changed", ownLastChanged)
			fmt.Println("Different node last updated", differentNodeLastUpdated)
			prettyOwnNode, err := json.MarshalIndent(networkMemberInfoMap[differentNode.PeerId], "", "  ")
			if err != nil {
				log.Println("Error marshalling own node", err)
			}
			log.Println("Pretty own node", string(prettyOwnNode))

			if ownLastChanged.After(differentNodeLastUpdated) || ownLastChanged.Equal(differentNodeLastUpdated) {
				continue
			}
			fmt.Println("Updating node", differentNode.Name)
			var nodeUpdate database.Node
			DB.Where("peer_id = ?", differentNode.PeerId).Preload("Addresses").First(&nodeUpdate)
			existingAddressIds := make([]uint, len(nodeUpdate.Addresses))
			for i, addr := range nodeUpdate.Addresses {
				existingAddressIds[i] = addr.ID
			}
			nodeUpdate.NodeName = differentNode.Name
			nodeUpdate.LastChanged = differentNode.LastUpdated
			// create new adresses, then delete old ones
			for i := len(differentNode.Addresses) - 1; i >= 0; i-- {
				addr := differentNode.Addresses[i]
				newAddress := database.NodeAddress{
					NodeID:  nodeUpdate.ID,
					Address: addr,
				}
				DB.Create(&newAddress)
				nodeUpdate.Addresses = append(nodeUpdate.Addresses, newAddress)
			}
			DB.Where("node_id = ? AND id IN (?)", nodeUpdate.ID, existingAddressIds).Delete(&database.NodeAddress{})
			DB.Save(&nodeUpdate)
		}

		networkMemberToSync.LastSync = time.Now()
		DB.Save(&networkMemberToSync)
	}
	h.NetworkSyncBlocker[networkName] = false

}
