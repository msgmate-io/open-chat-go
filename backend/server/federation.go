package server

/**
* NOTES:
* example how to parse stream as http request:
* https://github.com/libp2p/go-libp2p/blob/7ce9c5024bc4c91fa7a3420e9a542435b9af6831/examples/http-proxy/proxy.go
 */

import (
	"backend/api/federation"
	"backend/database"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
	"time"
)

func CreateHost(
	DB *gorm.DB,
	port int,
	randomness io.Reader,
) (host.Host, *federation.WhitelistGater, error) {
	// 0 - check if the key already exists
	var existingKey database.Key
	q := DB.First(&existingKey, "key_type = ? AND key_name = ?", "private", "libp2p")

	var prvKey crypto.PrivKey
	var err error
	if q.Error == nil {
		// create crypto.PrivKey from the db 'existingKey.KeyContent'
		log.Printf("Using existing private key found in DB!")
		prvKey, err = crypto.UnmarshalRsaPrivateKey(existingKey.KeyContent)
	} else {
		prvKey, _, err = crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, randomness)
		privKeyBytes, _ := prvKey.Raw()

		key := database.Key{
			KeyType:    "private",
			KeyName:    "libp2p",
			KeyContent: privKeyBytes,
		}

		DB.Create(&key)
	}

	if err != nil {
		log.Println(err)
		return nil, nil, err
	}

	// TODO: allow resstriction listen address via param
	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port))
	fmt.Println("Host Listen Address:", sourceMultiAddr)

	// generally allow all peer id's that are registered
	var allPeerIds []string
	DB.Model(&database.Node{}).Pluck("peer_id", &allPeerIds)
	var allPeerIdsP2p []peer.ID
	for _, peerId := range allPeerIds {
		pID, err := peer.Decode(peerId)
		if err != nil {
			log.Printf("Failed to decode peer ID %s: %v", peerId, err)
			continue
		}
		allPeerIdsP2p = append(allPeerIdsP2p, pID)
	}

	fmt.Println("All Peer IDs:", allPeerIds, allPeerIdsP2p)

	gater := federation.NewWhitelistGater(allPeerIdsP2p)

	h, err := libp2p.New(
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(prvKey),
		libp2p.ConnectionGater(gater),
		// Enable stuff for hole punching etc...
	)

	return h, gater, err
}

func writePrivateKeyToFile(prvKey crypto.PrivKey, filename string) error {
	// Extract the private key bytes
	privKeyBytes, err := prvKey.Raw()

	if err != nil {
		return err
	}

	// Create a PEM block
	privPemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privKeyBytes,
	}

	// Create or overwrite the file
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the PEM block to the file
	if err := pem.Encode(file, privPemBlock); err != nil {
		return err
	}

	return nil
}

// CreateIncomingRequestStreamHandler creates a stream handler with configured host and port
func CreateIncomingRequestStreamHandler(host string, hostPort int) network.StreamHandler {
	return func(stream network.Stream) {
		defer stream.Close()

		buf := bufio.NewReader(stream)
		req, err := http.ReadRequest(buf)
		if err != nil {
			stream.Reset()
			log.Println(err)
			return
		}
		defer req.Body.Close()

		// Configure request URL with provided host and port
		fullHost := fmt.Sprintf("%s:%d", host, hostPort)
		req.URL.Scheme = "http"
		req.URL.Host = fullHost

		outreq := new(http.Request)
		*outreq = *req

		// Preserve the original request headers
		outreq.Header = req.Header

		fmt.Printf("[f-proxy] Making request to %s\n", req.URL)
		resp, err := http.DefaultTransport.RoundTrip(outreq)
		if err != nil {
			stream.Reset()
			log.Println(err)
			return
		}

		// Create a response writer that will write to the stream
		writer := bufio.NewWriter(stream)

		// Write the response status line
		fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))

		// Write all response headers
		for key, values := range resp.Header {
			for _, value := range values {
				fmt.Fprintf(writer, "%s: %s\r\n", key, value)
			}
		}

		// Write the empty line that separates headers from body
		fmt.Fprintf(writer, "\r\n")

		// Flush the headers
		writer.Flush()

		// Copy the response body
		io.Copy(writer, resp.Body)
		writer.Flush()
		resp.Body.Close()
	}
}

func StartRequestReceivingPeer(ctx context.Context, h host.Host, streamHandler network.StreamHandler) {
	// Set a function as stream handler.
	// This function is called when a peer connects, and starts a stream with this protocol.
	// Only applies on the receiving side.
	h.SetStreamHandler("/t1m-http-request/0.0.1", streamHandler)

	// Let's get the actual TCP port from our listen multiaddr, in case we're using 0 (default; random available port).
	var port string
	for _, la := range h.Network().ListenAddresses() {
		fmt.Println("Listen Address:", la)
		if p, err := la.ValueForProtocol(multiaddr.P_TCP); err == nil {
			fmt.Println("Actual local port is:", p)
			port = p
			break
		}
	}

	if port == "" {
		log.Println("was not able to find actual local port")
		return
	}
}

// Starts a libp2p host for this server node that can be dialed from any other open-chat node
func CreateFederationHost(
	DB *gorm.DB,
	host string,
	p2pPort int,
	hostPort int,
) (*host.Host, *federation.FederationHandler, error) {
	var r io.Reader
	r = rand.Reader

	h, gater, err := CreateHost(DB, p2pPort, r)
	gater.AddAllowedPeer(h.ID())
	fmt.Println("================", "Setting up Host Node", "================")
	fmt.Println("Host Identity:", h.ID())
	for _, addr := range h.Addrs() {
		fmt.Println("Host P2P Address(es):", addr)
	}
	if err != nil {
		log.Println(err)
		return nil, nil, err
	}

	// Start the peer
	StartRequestReceivingPeer(context.Background(), h, CreateIncomingRequestStreamHandler(host, hostPort))

	federationHandler := &federation.FederationHandler{
		Host:      h,
		AutoPings: make(map[string]context.CancelFunc),
		Gater:     gater,
	}

	return &h, federationHandler, nil
}

func StartProxies(DB *gorm.DB, h *federation.FederationHandler) {
	proxies := []database.Proxy{}
	q := DB.Preload("Node").Where("active = ?", true).Find(&proxies)

	if q.Error != nil {
		log.Println("Couldn't find proxies", q.Error)
		return
	}

	log.Println("Starting proxies for", len(proxies), "nodes")
	for _, proxy := range proxies {
		proxy.Node = database.Node{}
		DB.Preload("Addresses").First(&proxy.Node, proxy.NodeID)
		log.Println("Starting proxy for node", proxy.Node.NodeName, "on port", proxy.Port)
		go func(proxy database.Proxy) {
			http.ListenAndServe(fmt.Sprintf(":%s", proxy.Port), federation.CreateProxyHandler(h, DB, proxy.Port, proxy.Node))
		}(proxy)
	}
}

func PreloadPeerstore(DB *gorm.DB, h *federation.FederationHandler) error {
	nodes := []database.Node{}
	q := DB.Preload("Addresses").Find(&nodes)

	if q.Error != nil {
		log.Println("Couldn't find nodes", q.Error)
		return q.Error
	}

	fmt.Println("Preloading peerstore for", len(nodes), "nodes")
	for _, node := range nodes {
		fmt.Println("Preloading peerstore for node", node.NodeName)
		for _, address := range node.Addresses {
			fmt.Println("Preloading peerstore for address", address.Address)
			maddr, err := multiaddr.NewMultiaddr(address.Address)
			if err != nil {
				log.Println("Couldn't parse address", address.Address, err)
				return err
			}
			info, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				log.Println("Couldn't parse address", address.Address, err)
				return err
			}
			fmt.Println("Preloading peerstore for address", info.ID, info.Addrs)
			h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
		}
		// Also send a generic ping to each node
		fmt.Println("Sending 'start-up' ping to node", node.UUID)
		ownPeerId := h.Host.ID().String()
		federation.SendNodeRequest(DB, h, node.UUID, federation.RequestNode{
			Path: "/api/v1/federation/nodes/" + ownPeerId + "/ping",
		})

		// TODO: make Ping time configurable an starting auto-ping optional
		err, cancel := federation.StartNodeAutoPing(DB, node.UUID, h, 60*time.Second)
		if err != nil {
			log.Println("Couldn't start node auto ping", err)
			return err
		}

		h.AutoPings[node.UUID] = cancel
	}

	return nil
}
