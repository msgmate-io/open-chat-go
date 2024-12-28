package federation

import (
	"backend/database"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
	"io"
	"log"
	"net"
	"time"
)

// connWrapper wraps a net.Conn and overrides its Read 5432
type connWrapper struct {
	net.Conn
	reader io.Reader
}

// Read implements io.Reader using the wrapped reader
func (c *connWrapper) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func CreateProxyHandlerTCP(h *FederationHandler, DB *gorm.DB, localPort string, node database.Node, protocolID protocol.ID, tlsConfig *tls.Config) (func(listener net.Listener), net.Listener, error) {
	log.Println(fmt.Sprintf("Creating TCP proxy to federated node: %s", node.PeerID))

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
	listenerNew, err := net.Listen("tcp", fmt.Sprintf(":%s", localPort))
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

			go handleTCPConnection(h, conn, info.ID, tlsConfig, protocolID)
		}
	}, listenerNew, nil
}

func handleTCPConnection(h *FederationHandler, clientConn net.Conn, peerID peer.ID, tlsConfig *tls.Config, protocolID protocol.ID) {
	defer clientConn.Close()

	log.Printf("Received connection from %s", clientConn.RemoteAddr())

	if tlsConfig != nil {
		// Read the first byte to check if it's a PostgreSQL SSL request
		firstByte := make([]byte, 1)
		n, err := clientConn.Read(firstByte)
		if err != nil {
			log.Printf("Error reading first byte: %v", err)
			return
		}

		var isPostgres bool
		var prefixedConn io.Reader = clientConn

		if n == 1 && firstByte[0] == 0 {
			// Read the rest of what might be a PostgreSQL SSL request
			restOfMessage := make([]byte, 7)
			_, err := io.ReadFull(clientConn, restOfMessage)
			if err != nil {
				log.Printf("Error reading rest of SSL request: %v", err)
				return
			}

			// Verify it's a PostgreSQL SSL request
			if isPostgreSQLSSLRequest(append(firstByte[:], restOfMessage...)) {
				log.Println("Handling PostgreSQL SSL request")
				isPostgres = true
				// Respond with 'S' to indicate we accept SSL
				_, err = clientConn.Write([]byte("S"))
				if err != nil {
					log.Printf("Error sending SSL acceptance: %v", err)
					return
				}
			} else {
				// Not a PostgreSQL request, treat as regular TLS
				prefixedConn = io.MultiReader(bytes.NewReader(append(firstByte[:], restOfMessage...)), clientConn)
			}
		} else if n == 1 {
			prefixedConn = io.MultiReader(bytes.NewReader(firstByte[:]), clientConn)
		}

		// Create wrapped connection
		wrappedConn := &connWrapper{
			Conn:   clientConn,
			reader: prefixedConn,
		}

		// For PostgreSQL, we need to wait for the actual SSL/TLS handshake after sending 'S'
		if isPostgres {
			// Don't use the prefixed reader for PostgreSQL as we've already handled the SSL request
			wrappedConn.reader = clientConn
		}

		// Upgrade to TLS connection
		tlsConn := tls.Server(wrappedConn, tlsConfig)

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
		clientConn = tlsConn
	}

	// Open libp2p stream
	var stream network.Stream
	var err error
	fmt.Println("Opening stream to peer:", peerID, "with protocol:", protocolID)
	stream, err = h.Host.NewStream(context.Background(), peerID, protocolID)
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

// Helper function to verify PostgreSQL SSL request
func isPostgreSQLSSLRequest(data []byte) bool {
	if len(data) != 8 {
		return false
	}
	// PostgreSQL SSL request format:
	// Length (4 bytes) = 8
	// Request Code (4 bytes) = 80877103
	return data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 8 &&
		data[4] == 0x04 && data[5] == 0xd2 && data[6] == 0x16 && data[7] == 0x2f
}

func copyStream(closer chan struct{}, dst io.Writer, src io.Reader) {
	defer func() { closer <- struct{}{} }() // connection is closed, send signal to stop proxy
	io.Copy(dst, src)
}

func CreateLocalTCPProxyHandler(h *FederationHandler, networkName string, trafficTarget string) network.StreamHandler {
	return func(stream network.Stream) {
		// The requesting peer *MUST* be registered in the network
		// remotePeerID := stream.Conn().RemotePeer()
		// fmt.Println("Network name:", networkName, "Peer ID:", stream.Conn().RemotePeer().String(), "Network peer ids:", h.NetworkPeerIds[networkName])
		if !h.NetworkPeerIds[networkName][stream.Conn().RemotePeer().String()] {
			log.Println("Peer not in network!", stream.Conn().RemotePeer().String(), h.NetworkPeerIds[networkName])
			stream.Reset()
			return
		}

		fmt.Printf("Connecting to '%s'\n", trafficTarget)
		c, err := net.Dial("tcp", trafficTarget)
		if err != nil {
			fmt.Printf("Reset %s: %s\n", stream.Conn().RemotePeer().String(), err.Error())
			stream.Reset()
			return
		}
		closer := make(chan struct{}, 2)
		go copyStream(closer, stream, c)
		go copyStream(closer, c, stream)
		<-closer

		stream.Close()
		c.Close()
		fmt.Printf("(service %s) Handled correctly '%s'\n", trafficTarget, stream.Conn().RemotePeer().String())
	}
}
