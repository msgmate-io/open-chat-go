package federation

import (
	"backend/database"
	"backend/server/util"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
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
	log.Println("Sending request to node:", node.PeerID, "at", data.Path, "method", data.Method)

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

	err = req.Write(stream)
	if err != nil {
		stream.Reset()
		log.Println(err)
		return nil, fmt.Errorf("Couldn't write request to stream")
	}

	// Now we read the response that was sent from the dest peer
	buf := bufio.NewReader(stream)
	resp, err := http.ReadResponse(buf, req)
	if err != nil {
		stream.Reset()
		log.Println(err)
		return nil, fmt.Errorf("Couldn't read response from stream")
	}

	// Ensure the response body is fully read
	defer resp.Body.Close()

	// Create a buffer to store the full response body
	var fullBody []byte
	fullBody, err = io.ReadAll(resp.Body)
	if err != nil {
		stream.Reset()
		log.Println(err)
		return nil, fmt.Errorf("Couldn't read full response body from stream")
	}

	// Create a new response with the full body
	resp.Body = io.NopCloser(bytes.NewReader(fullBody))

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

func (h *FederationHandler) RequestNodeByPeerId(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	peerId := r.PathValue("peer_id")
	if peerId == "" {
		http.Error(w, "Invalid peer ID", http.StatusBadRequest)
		return
	}

	var data RequestNode
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var node database.Node
	q := DB.Preload("Addresses").Where("peer_id = ?", peerId).First(&node)
	if q.Error != nil {
		http.Error(w, "Couldn't find node with that peer ID", http.StatusBadRequest)
		return
	}

	resp, err := SendRequestToNode(h, node, data, T1mNetworkRequestProtocolID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Println("Response ARRIVE HERE:", resp, resp.Header)

	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// Check if the response is an octet stream
	if false && resp.Header.Get("Content-Type") == "application/octet-stream" {
		// Create a temporary file
		tempFile, err := os.CreateTemp("", "response-*.tmp")
		if err != nil {
			log.Println("Error creating temporary file:", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer tempFile.Close()

		// Copy the response body to the temporary file
		if _, err := io.Copy(tempFile, resp.Body); err != nil {
			log.Println("Error copying response body to file:", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Optionally, you can log the file path or perform further operations
		log.Println("Response body written to temporary file:", tempFile.Name())
	} else {
		// Copy the response body to the client
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Println("Error copying response body:", err)
		}
	}

	// Ensure the response body is closed
	if err := resp.Body.Close(); err != nil {
		log.Println("Error closing response body:", err)
	}
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
