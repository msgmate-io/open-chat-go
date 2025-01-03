package federation

import (
	"backend/database"
	"backend/server/util"
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

	// Retrieve the node
	var node database.Node

	q := DB.Preload("Addresses").Where("uuid = ?", nodeUuid).First(&node)

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

	// TODO: Loop trough an try all adresses!
	// TODO: possibly re-order by 'reachability'

	// (3) - Connect to all federation nodes
	prettyFederationNode, err := json.MarshalIndent(node, "", "  ")
	log.Println("Connecting to Federation Node:", string(prettyFederationNode))
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

	log.Println("Starting Federation Peer ID:", info.ID, info.Addrs, "HOST", h.Host)

	// Register address in peerstore TODO: first check if that peer is already present in the peerstore
	h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

	stream, err := h.Host.NewStream(context.Background(), info.ID, "/t1m-http-request/0.0.1")
	if err != nil {
		log.Println("Error opening stream to node:", err)
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
