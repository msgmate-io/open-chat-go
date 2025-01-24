package federation

import (
	"backend/database"
	"backend/server/util"
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"fmt"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
)

type RequestNode struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func SendRequestToNode(h *FederationHandler, node database.Node, data RequestNode, protocolName string) (*http.Response, error) {
	// now we build a new request based on the data
	req, err := http.NewRequest(data.Method, data.Path, strings.NewReader(data.Body))
	if err != nil {
		return nil, fmt.Errorf("Couldn't create request")
	}

	// Fill the request with headers
	for k, v := range data.Headers {
		req.Header.Add(k, v)
	}

	// (3) - Connect to all federation nodes
	log.Println("Sending request to node:", node.PeerID, "at", data.Path)

	var info *peer.AddrInfo
	for _, address := range node.Addresses {
		maddr, err := multiaddr.NewMultiaddr(address.Address)
		if err != nil {
			return nil, fmt.Errorf("Invalid address")
		}

		info, err = peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return nil, fmt.Errorf("Invalid address")
		}

		h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	}

	stream, err := h.Host.NewStream(context.Background(), info.ID, protocol.ID(protocolName))
	if err != nil {
		log.Println("Error opening stream to node:", err)
		return nil, fmt.Errorf("Couldn't open stream to node")
	}

	defer stream.Close()

	//log.Println("Sending request to node ( writing to stream now ):", node)

	// r.Write() writes the HTTP request to the stream.
	err = req.Write(stream)
	if err != nil {
		stream.Reset()
		log.Println(err)
		return nil, fmt.Errorf("Couldn't write request to stream")
	}

	// Now we read the response that was sent from the dest
	// peer
	buf := bufio.NewReader(stream)
	resp, err := http.ReadResponse(buf, req)
	if err != nil {
		stream.Reset()
		log.Println(err)
		return nil, fmt.Errorf("Couldn't read response from stream")
	}

	return resp, nil
}

func SendNodeRequest(DB *gorm.DB, h *FederationHandler, nodeUUID string, data RequestNode) (*http.Response, error) {
	// Retrieve the node
	var node database.Node
	q := DB.Preload("Addresses").Where("uuid = ?", nodeUUID).First(&node)

	if q.Error != nil {
		return nil, fmt.Errorf("Couldn't find node with that UUID")
	}

	return SendRequestToNode(h, node, data, T1mNetworkJoinProtocolID)
}

func (h *FederationHandler) RequestNode(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
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

	var data RequestNode
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	resp, err := SendNodeRequest(DB, h, nodeUuid, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Write response status and headers
	w.WriteHeader(resp.StatusCode)

	// Finally copy the body
	io.Copy(w, resp.Body)
	resp.Body.Close()
}
