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

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
)

func CreateProxyHandler(h *FederationHandler, DB *gorm.DB, localPort string, node database.Node) http.HandlerFunc {
	prettyFederationNode, err := json.MarshalIndent(node, "", "  ")
	if err != nil {
		log.Println("Couldn't marshal node", err)
		return nil
	}

	log.Println("Creating proxy to federated node:", string(prettyFederationNode))

	var info *peer.AddrInfo
	for _, address := range node.Addresses {
		maddr, err := multiaddr.NewMultiaddr(address.Address)
		if err != nil {
			log.Println("Invalid address", address.Address)
			return nil
		}

		info, err = peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			log.Println("Invalid address", address.Address)
			return nil
		}

		log.Println("Registering Federation Peer ID:", info.ID, info.Addrs, "HOST", h.Host)
		log.Println("Peerstore:", h.Host.Peerstore().Peers().String())
		for _, p := range h.Host.Peerstore().Peers() {
			// log all peers addresses
			log.Println("--> Peers adresses:", h.Host.Peerstore().Addrs(p))
		}
		h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// here we try to proxy the request to a specific node

		// Replace this line:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body", http.StatusInternalServerError)
			return
		}
		req, err := http.NewRequest(r.Method, r.URL.Path, strings.NewReader(string(body)))

		if err != nil {
			http.Error(w, "Couldn't create request", http.StatusInternalServerError)
			return
		}

		// Fill the request with headers
		for k, v := range r.Header {
			req.Header.Add(k, strings.Join(v, ","))
		}

		req.Header.Add("X-Proxy-Route", node.Addresses[0].Address)
		req.Header.Add("X-Proxy-Local-Port", localPort)
		req.Header.Add("X-Proxy-Node-UUID", node.UUID)

		stream, err := h.Host.NewStream(context.Background(), info.ID, "/t1m-http-request/0.0.1")
		if err != nil {
			log.Println("Error opening stream to node:", err)
			log.Println("Peerstore:", h.Host.Peerstore().Peers().String())
			for _, p := range h.Host.Peerstore().Peers() {
				// log all peers addresses
				log.Println("--> Peers adresses:", h.Host.Peerstore().Addrs(p))
			}
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
}

func (h *FederationHandler) CreateAndStartProxy(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	nodeUuid := r.PathValue("node_uuid")
	if nodeUuid == "" {
		http.Error(w, "Invalid node UUID", http.StatusBadRequest)
		return
	}

	portS := r.PathValue("local_port")
	if portS == "" {
		http.Error(w, "Invalid port", http.StatusBadRequest)
		return
	}

	var node database.Node
	q := DB.Preload("Addresses").Where("uuid = ?", nodeUuid).First(&node)
	if q.Error != nil {
		log.Println("Couldn't find node with that UUID", nodeUuid)
		http.Error(w, "Couldn't find node with that UUID", http.StatusNotFound)
		return
	}

	proxy := database.Proxy{
		NodeID: node.ID,
		Node:   node,
		Port:   portS,
		Active: true,
	}
	// check if already exists
	q = DB.First(&proxy, "node_id = ? AND port = ?", node.ID, portS)
	if q.Error == nil {
		http.Error(w, "Proxy already exists and should be running!", http.StatusConflict)
		return
	}
	DB.Create(&proxy)

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/", CreateProxyHandler(h, DB, portS, node))
		http.ListenAndServe(":"+portS, mux)
	}()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Proxy created and started"))
}
