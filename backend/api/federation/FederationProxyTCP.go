package federation

import (
	"backend/database"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
	"io"
	"log"
	"net"
	"time"
)

func CreateProxyHandlerTCP(h *FederationHandler, DB *gorm.DB, localPort string, node database.Node, proxy database.Proxy, tlsConfig *tls.Config) (func(listener net.Listener), net.Listener, error) {
	prettyFederationNode, err := json.MarshalIndent(node, "", "  ")
	if err != nil {
		log.Println("Couldn't marshal node", err)
		return nil, nil, err
	}

	log.Println("Creating TCP proxy to federated node:", string(prettyFederationNode))

	var info *peer.AddrInfo
	for _, address := range node.Addresses {
		maddr, err := multiaddr.NewMultiaddr(address.Address)
		if err != nil {
			log.Println("Invalid address", address.Address)
			return nil, nil, err
		}

		info, err = peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			log.Println("Invalid address", address.Address)
			return nil, nil, err
		}

		log.Println("Registering Federation Peer ID:", info.ID, info.Addrs, "HOST", h.Host)
		h.Host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	}

	var listenerNew net.Listener
	// Always create a non-TLS listener first
	listenerNew, err = net.Listen("tcp", fmt.Sprintf(":%s", localPort))
	if err != nil {
		log.Printf("Failed to start TCP listener on port %s: %v", localPort, err)
		return nil, nil, err
	}

	return func(listener net.Listener) {
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("Error accepting connection: %v", err)
				continue
			}

			go handleTCPConnection(h, conn, info.ID, tlsConfig)
		}
	}, listenerNew, nil
}

func handleTCPConnection(h *FederationHandler, clientConn net.Conn, peerID peer.ID, tlsConfig *tls.Config) {
	defer clientConn.Close()

	log.Printf("Received connection from %s", clientConn.RemoteAddr())

	if tlsConfig != nil {
		// Read the first byte to check if it's a PostgreSQL SSL request
		firstByte := make([]byte, 1)
		_, err := clientConn.Read(firstByte)
		if err != nil {
			log.Printf("Error reading first byte: %v", err)
			return
		}

		// PostgreSQL SSL request message is 8 bytes long and starts with messageLength=8 (4 bytes) followed by requestCode=80877103 (4 bytes)
		if firstByte[0] == 0 {
			// This might be a PostgreSQL SSL request
			restOfMessage := make([]byte, 7)
			_, err := io.ReadFull(clientConn, restOfMessage)
			if err != nil {
				log.Printf("Error reading rest of SSL request: %v", err)
				return
			}

			log.Println("Received PostgreSQL SSL request")

			// Respond with 'S' to indicate we accept SSL
			_, err = clientConn.Write([]byte("S"))
			if err != nil {
				log.Printf("Error sending SSL acceptance: %v", err)
				return
			}

			log.Println("Sent SSL acceptance, upgrading to TLS")

			// Upgrade the connection to TLS
			tlsConn := tls.Server(clientConn, tlsConfig)

			// Set a deadline for the handshake
			tlsConn.SetDeadline(time.Now().Add(10 * time.Second))
			err = tlsConn.Handshake()
			// Clear the deadline
			tlsConn.SetDeadline(time.Time{})

			if err != nil {
				log.Printf("TLS handshake failed: %v", err)
				return
			}

			log.Printf("TLS handshake completed successfully. Protocol: %s", tlsConn.ConnectionState().NegotiatedProtocol)

			// Use the TLS connection for the rest of the communication
			clientConn = tlsConn
		} else {
			log.Printf("Not a PostgreSQL SSL request, first byte: %d", firstByte[0])
			return
		}
	}

	// Open libp2p stream
	stream, err := h.Host.NewStream(context.Background(), peerID, "/t1m-tcp-proxy/0.0.1")
	if err != nil {
		log.Printf("Error opening stream to peer: %v", err)
		return
	}
	defer stream.Close()

	// Create error channel to coordinate goroutines
	errChan := make(chan error, 2)

	// Forward data from client to peer
	go func() {
		_, err := io.Copy(stream, clientConn)
		errChan <- err
	}()

	// Forward data from peer to client
	go func() {
		_, err := io.Copy(clientConn, stream)
		errChan <- err
	}()

	// Wait for either direction to finish or error
	err = <-errChan
	if err != nil && err != io.EOF {
		log.Printf("Error in TCP proxy: %v", err)
	}
}
