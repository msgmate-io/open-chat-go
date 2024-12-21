package federation

import (
	"backend/database"
	"bufio"
	"context"
	"encoding/json"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"io"
	"log"
	"net/http"
	"strings"
)

type RequestNode struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func (h *FederationHandler) RequestNode(w http.ResponseWriter, r *http.Request) {
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

	// Retrieve the node
	var node database.Node

	q := database.DB.Preload("Addresses").Where("uuid = ?", nodeUuid).First(&node)

	if q.Error != nil {
		http.Error(w, "Couldn't find node with that UUID", http.StatusNotFound)
		return
	}

	var data RequestNode

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// now we build a new request based on the data
	req, err := http.NewRequest(data.Method, data.Path, strings.NewReader(data.Body))
	if err != nil {
		http.Error(w, "Couldn't create request", http.StatusInternalServerError)
		return
	}

	// Fill the request with headers
	for k, v := range data.Headers {
		req.Header.Add(k, v)
	}

	// (3) - Connect to all federation nodes
	log.Println("Connecting to Federation Node:", node)
	maddr, err := multiaddr.NewMultiaddr(node.Addresses[0].Address)
	if err != nil {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	log.Println("Starting Federation Peer ID:", info.ID, info.Addrs, "HOST", FederationHost)

	// Register address in peerstore TODO: first check if that peer is already present in the peerstore
	FederationHost.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

	stream, err := FederationHost.NewStream(context.Background(), info.ID, "/t1m-http-request/0.0.1")
	if err != nil {
		http.Error(w, "Couldn't open stream to node", http.StatusInternalServerError)
		return
	}

	defer stream.Close()

	log.Println("Sending request to node ( writing to stream now ):", node)

	// r.Write() writes the HTTP request to the stream.
	err = req.Write(stream)
	if err != nil {
		stream.Reset()
		log.Println(err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// Now we read the response that was sent from the dest
	// peer
	buf := bufio.NewReader(stream)
	resp, err := http.ReadResponse(buf, r)
	if err != nil {
		stream.Reset()
		log.Println(err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// Write response status and headers
	w.WriteHeader(resp.StatusCode)

	// Finally copy the body
	io.Copy(w, resp.Body)
	resp.Body.Close()

}

// POST /api/federation/nodes/{node_uuid}/{api_path}
func (h *FederationHandler) PingNode(w http.ResponseWriter, r *http.Request) {
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

	apiPath := r.PathValue("api_path")
	if apiPath == "" {
		http.Error(w, "Invalid node UUID", http.StatusBadRequest)
		return
	}

	// Retrieve the node
	var node database.Node

	q := database.DB.Preload("Addresses").Where("uuid = ?", nodeUuid).First(&node)

	if q.Error != nil {
		http.Error(w, "Couldn't find node with that UUID", http.StatusNotFound)
		return
	}

	// Now open a stream to that node and send a ping message

	// (3) - Connect to all federation nodes
	log.Println("Connecting to Federation Node:", node)
	maddr, err := multiaddr.NewMultiaddr(node.Addresses[0].Address)
	if err != nil {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	log.Println("Starting Federation Peer ID:", info.ID, info.Addrs, "HOST", FederationHost)

	// Register address in peerstore TODO: first check if that peer is already present in the peerstore
	FederationHost.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

	stream, err := FederationHost.NewStream(context.Background(), info.ID, "/t1m-http-request/0.0.1")
	if err != nil {
		http.Error(w, "Couldn't open stream to node", http.StatusInternalServerError)
		return
	}

	defer stream.Close()

	// r.Write() writes the HTTP request to the stream.
	err = r.Write(stream)
	if err != nil {
		stream.Reset()
		log.Println(err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// Now we read the response that was sent from the dest
	// peer
	buf := bufio.NewReader(stream)
	resp, err := http.ReadResponse(buf, r)
	if err != nil {
		stream.Reset()
		log.Println(err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// copy only headers that start with `X-Forward-`
	// remove ALL other headers
	for k, v := range resp.Header {
		if strings.HasPrefix(k, "X-Forward-") {
			newKey := strings.TrimPrefix(k, "X-Forward-")
			for _, s := range v {
				w.Header().Add(newKey, s)
			}
		}
	}

	// modify the request url to be api_path

	// Write response status and headers
	w.WriteHeader(resp.StatusCode)

	// Finally copy the body
	io.Copy(w, resp.Body)
	resp.Body.Close()
}

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
