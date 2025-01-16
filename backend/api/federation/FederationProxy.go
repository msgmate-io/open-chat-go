package federation

import (
	"backend/database"
	"backend/server/util"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
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
		// Create a pipe to stream the request body
		pr, pw := io.Pipe()

		// Copy the request body to the pipe writer in a goroutine
		go func() {
			defer pw.Close()
			io.Copy(pw, r.Body)
		}()

		// Create new request with the pipe reader as the body
		req, err := http.NewRequest(r.Method, r.URL.Path, pr)
		if err != nil {
			http.Error(w, "Couldn't create request", http.StatusInternalServerError)
			return
		}

		// Copy original headers
		for k, v := range r.Header {
			req.Header[k] = v
		}

		req.Header.Add("X-Proxy-Local-Port", localPort)
		req.Header.Add("X-Proxy-Node-UUID", node.UUID)

		// Open libp2p stream
		stream, err := h.Host.NewStream(context.Background(), info.ID, "/t1m-http-request/0.0.1")
		if err != nil {
			log.Println("Error opening stream to node:", err)
			http.Error(w, "Couldn't open stream to node", http.StatusInternalServerError)
			return
		}
		defer stream.Close()

		// Write request to stream
		if err := req.Write(stream); err != nil {
			stream.Reset()
			log.Println(err)
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}

		// Read response
		resp, err := http.ReadResponse(bufio.NewReader(stream), req)
		if err != nil {
			stream.Reset()
			log.Println(err)
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		fmt.Println("PROXY ===> RESPONSE HEADERS:", resp.Header)
		// Copy response headers
		for k, v := range resp.Header {
			if k == "Set-Cookie" {
				// Rewrite cookie domain to requesting host
				for _, cookie := range v {
					c, err := http.ParseSetCookie(cookie)
					if err == nil {
						// For IP addresses, don't set the domain at all
						host := r.Host
						if i := strings.Index(host, ":"); i != -1 {
							host = host[:i]
						}
						if net.ParseIP(host) != nil {
							c.Domain = ""
						} else {
							c.Domain = host
						}
						// Convert back to header string
						w.Header().Add(k, c.String())
					} else {
						fmt.Println("PROXY ===> COOKIE ERROR:", err)
					}
				}
			} else {
				w.Header()[k] = v
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Stream the response body
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Println("Error copying response:", err)
		}
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
