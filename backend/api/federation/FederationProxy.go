package federation

import (
	"backend/database"
	"backend/server/util"
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func CreateProxyHandlerHTTP(h *FederationHandler, DB *gorm.DB, localPort string, node database.Node, proxy database.Proxy) http.HandlerFunc {
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
		req.Header.Add("X-Proxy-Destination", fmt.Sprintf("%s://%s", "http", "localhost:5432"))
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
		// Modifies the domain of `Set-Cookie` headers to the requesting host
		for k, v := range resp.Header {
			if k == "Set-Cookie" {
				for _, cookie := range v {
					c, err := http.ParseSetCookie(cookie)
					if err == nil {
						host := r.Host
						if i := strings.Index(host, ":"); i != -1 {
							host = host[:i]
						}
						if net.ParseIP(host) != nil {
							c.Domain = ""
						} else {
							c.Domain = host
						}
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

		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Println("Error copying response:", err)
		}
	}
}

type CreateAndStartProxyRequest struct {
	UseTLS        bool   `json:"use_tls"`
	KeyPrefix     string `json:"key_prefix"`
	NodeUUID      string `json:"node_uuid"`
	Port          string `json:"port"`
	Kind          string `json:"kind"`
	Direction     string `json:"direction"`
	TrafficOrigin string `json:"traffic_origin"`
	TrafficTarget string `json:"traffic_target"`
	NetworkName   string `json:"network_name"`
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

	var req CreateAndStartProxyRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Direction == "" {
		req.Direction = "egress"
	}

	if req.Kind == "" {
		req.Kind = "http"
	}

	if req.NetworkName == "" {
		req.NetworkName = "network"
	}

	// TODO: allow configuring this trough the request
	in5min := time.Now().Add(5 * time.Minute)
	proxy := database.Proxy{
		Port:          req.Port,
		Active:        true,
		UseTLS:        req.UseTLS,
		Kind:          req.Kind,
		Direction:     req.Direction,
		NetworkName:   req.NetworkName,
		TrafficOrigin: req.TrafficOrigin,
		TrafficTarget: req.TrafficTarget,
		// per default expire 5 minutes after creation!
		ExpiresAt: &in5min,
	}

	if proxy.TrafficOrigin == "" && proxy.Direction == "egress" {
		proxy.TrafficOrigin = fmt.Sprintf("%s:%s", h.Host.ID().String(), proxy.Port)
	}

	if proxy.TrafficTarget == "" && proxy.Direction == "ingress" {
		proxy.TrafficTarget = fmt.Sprintf("%s:%s", h.Host.ID().String(), proxy.Port)
	}

	q := DB.First(&proxy, "traffic_origin = ? AND traffic_target = ?", proxy.TrafficOrigin, proxy.TrafficTarget)
	if q.Error == nil {
		// if it already exists we just return OK
		http.Error(w, "Proxy already exists and should be running!", http.StatusOK)
		return
	}

	DB.Create(&proxy)

	originData := strings.Split(proxy.TrafficOrigin, ":")
	originPort := originData[1]
	originPeerId := originData[0]
	targetData := strings.Split(proxy.TrafficTarget, ":")
	targetPort := targetData[1]
	targetPeerId := targetData[0]

	trafficTargetNode := database.Node{}
	DB.Where("peer_id = ?", targetPeerId).Preload("Addresses").First(&trafficTargetNode)

	trafficOriginNode := database.Node{}
	DB.Where("peer_id = ?", originPeerId).Preload("Addresses").First(&trafficOriginNode)

	protocolID := CreateT1mTCPTunnelProtocolID(originPort, originPeerId, targetPort, targetPeerId)

	if proxy.Direction == "egress" {
		if proxy.Kind == "tcp" {
			err = h.StartEgressProxy(DB, proxy, trafficTargetNode, trafficOriginNode, originPort, req.KeyPrefix, protocolID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else if proxy.Kind == "ssh" {
			if proxy.Port == "" {
				http.Error(w, "Port is required for SSH proxy", http.StatusBadRequest)
				return
			}
			if originPeerId != "ssh" {
				http.Error(w, "Origin peer ID must be 'ssh' for SSH proxy", http.StatusBadRequest)
				return
			}
			portNum, err := strconv.Atoi(proxy.Port)
			if err != nil {
				http.Error(w, "Port must be a valid number for SSH proxy", http.StatusBadRequest)
				return
			}
			h.StartSSHProxy(portNum, originPort)
		} else if proxy.Kind == "video" {
			/**
			portNum, err := strconv.Atoi(proxy.Port)
			if err != nil {
				http.Error(w, "Port must be a valid number for video proxy", http.StatusBadRequest)
				return
			}
			LinuxStartVideoServer(portNum, originPeerId)
			**/
		}
	} else { // Ingress!
		fmt.Println("Starting proxy for node on port", proxy.Port, "with protocol ID", protocolID)
		targetSem := fmt.Sprintf(":%s", targetPort)
		h.Host.SetStreamHandler(protocolID, CreateLocalTCPProxyHandler(h, proxy.NetworkName, targetSem))
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Proxy created and started"))
}

func (h *FederationHandler) AutoRemoveExpiredProxies(DB *gorm.DB) {
	// every 5 min remove expired proxies
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		err := h.RemoveExpiredProxies(DB)
		if err != nil {
			log.Println("Couldn't remove expired proxies", err)
		}
	}
}

func (h *FederationHandler) RemoveExpiredProxies(DB *gorm.DB) error {
	expiredProxies := []database.Proxy{}
	q := DB.Where("expires_at < ? AND expires_at IS NOT NULL", time.Now()).Find(&expiredProxies)
	if q.Error != nil {
		log.Println("No proxies that are expired!")
		return nil
	}
	for _, proxy := range expiredProxies {
		DB.Delete(&proxy)
		log.Println("Removed expired proxy", proxy.ID)
	}
	return nil
}

func (h *FederationHandler) StartEgressProxy(
	DB *gorm.DB,
	proxy database.Proxy,
	trafficTargetNode database.Node,
	trafficOriginNode database.Node,
	originPort string,
	keyPrefix string,
	protocolID protocol.ID,
) error {
	var certPEM, keyPEM, issuerPEM database.Key
	var certPEMBytes, keyPEMBytes []byte
	if proxy.UseTLS {
		// for tls proviving keyPrefix is required!
		q := DB.Where("key_type = ? AND key_name = ?", "cert", fmt.Sprintf("%s_cert.pem", keyPrefix)).First(&certPEM)
		if q.Error != nil {
			return fmt.Errorf("Couldn't find cert key for node, if you want to use TLS for this proxy create the keys first!")
		}
		q = DB.Where("key_type = ? AND key_name = ?", "key", fmt.Sprintf("%s_key.pem", keyPrefix)).First(&keyPEM)
		if q.Error != nil {
			return fmt.Errorf("Couldn't find key key for node, if you want to use TLS for this proxy create the keys first!")
		}
		q = DB.Where("key_type = ? AND key_name = ?", "issuer", fmt.Sprintf("%s_issuer.pem", keyPrefix)).First(&issuerPEM)
		if q.Error != nil {
			return fmt.Errorf("Couldn't find issuer key for node, if you want to use TLS for this proxy create the keys first!")
		}

		certPEMBytes = certPEM.KeyContent
		keyPEMBytes = keyPEM.KeyContent
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.ActiveProxies[proxy.UUID] = cancel

	go func() {
		defer func() {
			delete(h.ActiveProxies, proxy.UUID)
		}()

		var handlerFunc func(listener net.Listener)
		var listener net.Listener
		var err error

		if proxy.UseTLS {
			cert, err := tls.X509KeyPair(certPEMBytes, keyPEMBytes)
			if err != nil {
				log.Printf("Error loading certificates: %v", err)
				return
			}
			tlsConfig := &tls.Config{
				Certificates: []tls.Certificate{cert},
			}
			handlerFunc, listener, err = CreateProxyHandlerTCP(h, DB, originPort, trafficTargetNode, protocolID, tlsConfig)
		} else {
			handlerFunc, listener, err = CreateProxyHandlerTCP(h, DB, originPort, trafficTargetNode, protocolID, nil)
		}

		if err != nil {
			log.Printf("Error creating TCP proxy: %v", err)
			return
		}

		done := make(chan struct{})

		go func() {
			handlerFunc(listener)
			close(done)
		}()

		select {
		case <-ctx.Done():
			listener.Close()
		case <-done:
		}
	}()

	return nil
}

type PaginatedProxies struct {
	database.Pagination
	Rows []database.Proxy `json:"rows"`
}

func (h *FederationHandler) ListProxies(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 10}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	var proxies []database.Proxy
	q := DB.Scopes(database.Paginate(&proxies, &pagination, DB)).
		Find(&proxies)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	pagination.Rows = proxies

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(pagination)
}
