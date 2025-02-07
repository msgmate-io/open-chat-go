package server

/**
* NOTES:
* example how to parse stream as http request:
* https://github.com/libp2p/go-libp2p/blob/7ce9c5024bc4c91fa7a3420e9a542435b9af6831/examples/http-proxy/proxy.go
 */

import (
	"backend/api/federation"
	"backend/database"
	"context"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
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
		// libp2p.EnableRelay(),
		libp2p.EnableRelay(),
		// libp2p.PrivateNetwork(), TODO: enable this key option for extra protection layer in the future!
		/*libp2p.EnableAutoRelayWithStaticRelays([]peer.AddrInfo{
			{
				ID:    peer.ID("QmUQE8cu5zrNCWd9RqzzVAriCrdyHMqFDAU2Fhh8T4LBfx"),
				Addrs: []multiaddr.Multiaddr{multiaddr.StringCast("/ip4/89.58.25.188/tcp/39672/p2p/QmUQE8cu5zrNCWd9RqzzVAriCrdyHMqFDAU2Fhh8T4LBfx")},
			}, TODO!
		}),*/
		libp2p.EnableNATService(),
		libp2p.NATPortMap(),
		libp2p.EnableAutoNATv2(),
		libp2p.EnableRelayService(),
		// libp2p.ConnectionGater(gater), TODO: peer gating should be disabled for all bootstrap peers
		// TODO introduce option to toggle ConnectionGater
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

func StartRequestReceivingPeer(ctx context.Context, h host.Host, streamHandler network.StreamHandler, protocolID protocol.ID) {
	h.SetStreamHandler(protocolID, streamHandler)

	// Let's get the actual TCP port from our listen multiaddr, in case we're using 0 (default; random available port).
	// TODO: waht is the following usefull for again?
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
	if err != nil {
		log.Println(err)
		return nil, nil, err
	}

	gater.AddAllowedPeer(h.ID())
	fmt.Println("================", "Setting up Host Node", "================")
	fmt.Println("Host Identity:", h.ID())
	for _, addr := range h.Addrs() {
		fmt.Println("Host P2P Address(es):", addr)
	}

	// this the default protocol that is accessible to any outside node
	// It only exposes network join apis
	// Participating in any of the other protocols requires being a network member

	federationHandler := &federation.FederationHandler{
		Host:               h,
		Gater:              gater,
		ActiveProxies:      make(map[string]context.CancelFunc),
		Networks:           make(map[string]database.Network),
		NetworkSyncs:       make(map[string]context.CancelFunc),
		NetworkSyncBlocker: make(map[string]bool),
		NetworkPeerIds:     make(map[string]map[string]bool),
	}

	StartRequestReceivingPeer(context.Background(), h, federationHandler.CreateIncomingRequestStreamHandler(host, hostPort, []string{
		"/api/v1/federation/networks/",
	}, ""), federation.T1mNetworkJoinProtocolID)

	return &h, federationHandler, nil
}

func StartProxies(DB *gorm.DB, h *federation.FederationHandler) {
	// first remove all expired proxies
	err := h.RemoveExpiredProxies(DB)
	if err != nil {
		log.Println("Couldn't remove expired proxies", err)
		return
	}

	egressProxies := []database.Proxy{}
	q := DB.Where("active = ? AND direction = ?", true, "egress").Find(&egressProxies)

	if q.Error != nil {
		log.Println("Couldn't find proxies", q.Error)
		return
	}

	log.Println("Starting 'egress' proxies for", len(egressProxies), "nodes")
	for _, proxy := range egressProxies {
		originData := strings.Split(proxy.TrafficOrigin, ":")
		originPort := originData[1]
		originPeerId := originData[0]
		targetData := strings.Split(proxy.TrafficTarget, ":")
		targetPort := targetData[1]
		targetPeerId := targetData[0]
		if proxy.Kind == "tcp" {
			node := database.Node{}
			DB.Where("peer_id = ?", targetPeerId).Preload("Addresses").First(&node)
			log.Println("Starting proxy for node", node.NodeName, "on port", proxy.Port)
			protocolID := federation.CreateT1mTCPTunnelProtocolID(originPort, originPeerId, targetPort, targetPeerId)
			h.StartEgressProxy(DB, proxy, node, node, originPort, proxy.NetworkName, protocolID)
		} else if proxy.Kind == "ssh" {
			portNum, err := strconv.Atoi(proxy.Port)
			if err != nil {
				log.Println("Cannot start SSH proxy, invalid port", proxy.Port, err)
				continue
			}
			h.StartSSHProxy(portNum, originPort) // here it holds the ssh password! TODO: we could actually extend this to store onl pw hashes too
		} else if proxy.Kind == "video" {
			/**
			portNum, err := strconv.Atoi(proxy.Port)
			if err != nil {
				log.Println("Cannot start video proxy, invalid port", proxy.Port, err)
				continue
			}
			federation.LinuxStartVideoServer(portNum, originPeerId)
			*/
		}
	}

	// Now start ingress proxies
	ingressProxies := []database.Proxy{}
	q = DB.Where("active = ? AND direction = ?", true, "ingress").Find(&ingressProxies)

	if q.Error != nil {
		log.Println("Couldn't find proxies", q.Error)
		return
	}

	log.Println("Starting 'ingress' proxies for", len(ingressProxies), "nodes")
	for _, proxy := range ingressProxies {

		originData := strings.Split(proxy.TrafficOrigin, ":")
		originPort := originData[1]
		originPeerId := originData[0]
		targetData := strings.Split(proxy.TrafficTarget, ":")
		targetPort := targetData[1]
		targetPeerId := targetData[0]

		node := database.Node{}
		DB.Where("peer_id = ?", targetPeerId).Preload("Addresses").First(&node)
		protocolID := federation.CreateT1mTCPTunnelProtocolID(originPort, originPeerId, targetPort, targetPeerId) // in this case 'network_name', 'traffic_target'
		fmt.Println("Starting proxy for node", node.NodeName, "on port", proxy.Port, "with protocol ID", protocolID)
		trafficTarget := fmt.Sprintf(":%s", targetPort)
		h.Host.SetStreamHandler(protocolID, federation.CreateLocalTCPProxyHandler(h, proxy.NetworkName, trafficTarget))
	}

	go h.AutoRemoveExpiredProxies(DB)
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
			// fmt.Println("Preloading peerstore for address", address.Address)
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
	}

	return nil
}

func InitializeNetworks(DB *gorm.DB, h *federation.FederationHandler, host string, hostPort int) {
	log.Println("Initializing networks")
	networks := []database.Network{}
	DB.Find(&networks)
	for _, network := range networks {
		log.Println("Initializing network", network.NetworkName)
		h.Networks[network.NetworkName] = network
		h.NetworkPeerIds[network.NetworkName] = map[string]bool{}
		networkMembers := []database.NetworkMember{}
		DB.Where("network_id = ?", network.ID).Preload("Node").Find(&networkMembers)
		for _, networkMember := range networkMembers {
			log.Println("Adding peer", networkMember.Node.PeerID, "to network: '", network.NetworkName, "'")
			h.AddNetworkPeerId(network.NetworkName, networkMember.Node.PeerID)
		}
		h.StartNetworkSyncProcess(DB, network.NetworkName)

		// start the network request protocol that allows querying any local api to network members only!
		StartRequestReceivingPeer(
			context.Background(), h.Host, h.CreateIncomingRequestStreamHandler(host, hostPort, []string{}, network.NetworkName),
			federation.T1mNetworkRequestProtocolID,
		)
	}

	fmt.Println("Network peer ids:", h.NetworkPeerIds)
}
