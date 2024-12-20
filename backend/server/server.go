package server

import (
	"backend/database"
	"bufio"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"log"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	"io"
	mrand "math/rand"
)

func CreateRootUser(username string, password string) {
	// first chaeck if that user already exists
	var user database.User
	database.DB.First(&user, "email = ?", username)

	if user.ID != 0 {
		log.Fatal("User already exists")
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

	if err != nil {
		log.Fatal(err)
	}

	user = database.User{
		Name:         username,
		Email:        username,
		PasswordHash: string(hashedPassword),
		ContactToken: uuid.New().String(),
		IsAdmin:      false,
	}

	q := database.DB.Create(&user)

	if q.Error != nil {
		log.Fatal(q.Error)
	}
}

var FederationHost host.Host

func GenerateToken(email string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(email), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Hash to store:", string(hash))

	hasher := md5.New()
	hasher.Write(hash)
	return hex.EncodeToString(hasher.Sum(nil))
}

func makeHost(port int, randomness io.Reader) (host.Host, error) {
	// Creates a new RSA key pair for this host.
	prvKey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, randomness)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// TODO: allow resstriction listen address via param
	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port))
	fmt.Println("Host Listen Address:", sourceMultiAddr)

	return libp2p.New(
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(prvKey),
		// Enable stuff for hole punching etc...
	)
}

func startPeer(ctx context.Context, h host.Host, streamHandler network.StreamHandler) {
	// Set a function as stream handler.
	// This function is called when a peer connects, and starts a stream with this protocol.
	// Only applies on the receiving side.
	h.SetStreamHandler("/chat/1.0.0", streamHandler)

	// Let's get the actual TCP port from our listen multiaddr, in case we're using 0 (default; random available port).
	var port string
	for _, la := range h.Network().ListenAddresses() {
		fmt.Println("Listen Address:", la)
		if p, err := la.ValueForProtocol(multiaddr.P_TCP); err == nil {
			port = p
			break
		}
	}

	if port == "" {
		log.Println("was not able to find actual local port")
		return
	}
}

func readData(rw *bufio.ReadWriter) {
	for {
		str, err := rw.ReadString('\n')
		if err != nil {
			log.Printf("Error reading from stream: %v\n", err)
			return
		}

		if str == "" {
			return
		}
		if str != "\n" {
			fmt.Printf("Received message: %s", str)
		}
	}
}

func writeData(rw *bufio.ReadWriter, message string) error {
	if _, err := rw.WriteString(fmt.Sprintf("%s\n", message)); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	return nil
}

func handleStream(s network.Stream) {
	log.Println("Got a new stream!")
	rw := bufio.NewReadWriter(bufio.NewReader(s), bufio.NewWriter(s))
	go readData(rw)
	go writeData(rw, "Connected sucessfully, hello from host "+FederationHost.ID().String())
}

func StartP2PFederation(
	port int,
	createTestNodes bool,
	rootNodeHost bool,
	bootstrapPeerNodes []string,
) {
	const debug bool = true
	// 0 - Create Test Nodes
	if createTestNodes {
		var nodeAdresses []database.NodeAddress
		for _, address := range bootstrapPeerNodes {
			fmt.Println("Registered Bootstrap Peer:", address)
			nodeAdresses = append(nodeAdresses, database.NodeAddress{
				Address: address,
			})
		}

		if len(nodeAdresses) > 0 {
			fmt.Println("Node Addresses:", nodeAdresses)
			var node = database.Node{
				NodeName:  "Node 1",
				Addresses: nodeAdresses,
			}
			database.DB.Create(&node)
		} else {
			fmt.Println("No Bootstrap Peers")
		}
	}

	// 1 - Get all 'Nodes'
	var nodes []database.Node

	//database.DB.Find(&nodes)

	database.DB.Preload("Addresses").Find(&nodes)

	for _, node := range nodes {
		fmt.Println("Node:", node.NodeName)
		for _, address := range node.Addresses {
			fmt.Println("Address:", address.Address)
		}
	}

	if rootNodeHost {
		// Create a p2p host
		var r io.Reader
		if debug {
			r = mrand.New(mrand.NewSource(int64(port)))
		} else {
			r = rand.Reader
		}

		h, err := makeHost(port, r)
		fmt.Println("================", "Setting up Host Node", "================")
		fmt.Println("Host Identity:", h.ID())
		p2pAdress := fmt.Sprintf("%s/p2p/%s", h.Addrs()[0], h.ID())
		fmt.Println("P2P Address:", p2pAdress)
		if err != nil {
			log.Println(err)
			return
		}

		// Start the peer
		startPeer(context.Background(), h, handleStream)
		FederationHost = h
		fmt.Println("================", "================", "================")

	}

	streams := make(map[string]network.Stream)
	for federationNode := range nodes {
		// (3) - Connect to all federation nodes
		fmt.Println("Connecting to Federation Node:", nodes[federationNode])
		maddr, err := multiaddr.NewMultiaddr(nodes[federationNode].Addresses[0].Address)
		if err != nil {
			panic(err)
		}

		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			panic(err)
		}

		fmt.Println("Starting Federation Peer ID:", info.ID, info.Addrs, "HOST", FederationHost)

		// Register address in peerstore
		FederationHost.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

		stream, err := FederationHost.NewStream(context.Background(), info.ID, "/chat/1.0.0")
		if err != nil {
			panic(err)
		}

		streams[info.ID.String()] = stream

		rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))

		writeData(rw, "Hello World from "+FederationHost.ID().String())
		go readData(rw)

	}
}

func BackendServer(
	host string,
	port int64,
	debug bool,
	ssl bool,
) (*http.Server, string) {
	var protocol string
	var fullHost string

	router := BackendRouting(debug)
	if ssl {
		protocol = "https"
	} else {
		protocol = "http"
	}

	fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: router,
	}

	return server, fullHost
}
