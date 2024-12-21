package server

/**
* NOTES:
* example how to parse stream as http request:
* https://github.com/libp2p/go-libp2p/blob/7ce9c5024bc4c91fa7a3420e9a542435b9af6831/examples/http-proxy/proxy.go
 */

import (
	"backend/api/federation"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/multiformats/go-multiaddr"
	"io"
	"log"
	mrand "math/rand"
	"net/http"
	"os"
	"strings"
)

func CreateHost(
	port int,
	randomness io.Reader,
	privateKeyPath string,
) (host.Host, error) {
	// 1 - Create a private key
	prvKey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, randomness)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// 2 - write the private key to privateKeyPath ( if != "" )
	if privateKeyPath != "" {
		writePrivateKeyToFile(prvKey, privateKeyPath)
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

func IncomingRequestStreamHander(stream network.Stream) {
	// Remember to close the stream when we are done.
	defer stream.Close()

	// Create a new buffered reader, as ReadRequest needs one.
	// The buffered reader reads from our stream, on which we
	// have sent the HTTP request (see ServeHTTP())
	buf := bufio.NewReader(stream)

	req, err := http.ReadRequest(buf)
	if err != nil {
		stream.Reset()
		log.Println(err)
		return
	}
	defer req.Body.Close()

	// We need to reset these fields in the request
	// URL as they are not maintained.
	fmt.Println("TBS, request url", req.URL)
	req.URL.Scheme = "http"
	hp := strings.Split(req.Host, ":")
	if len(hp) > 1 && hp[1] == "443" {
		req.URL.Scheme = "https"
	} else {
		req.URL.Scheme = "http"
	}
	req.URL.Host = req.Host

	outreq := new(http.Request)
	*outreq = *req

	// We now make the request
	fmt.Printf("Making request to %s\n", req.URL)
	resp, err := http.DefaultTransport.RoundTrip(outreq)
	if err != nil {
		stream.Reset()
		log.Println(err)
		return
	}

	// resp.Write writes whatever response we obtained for our
	// request back to the stream.
	resp.Write(stream)
}

func StartRequestReceivingPeer(ctx context.Context, h host.Host, streamHandler network.StreamHandler) {
	// Set a function as stream handler.
	// This function is called when a peer connects, and starts a stream with this protocol.
	// Only applies on the receiving side.
	h.SetStreamHandler("/http-request/1.0.0", streamHandler)

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

// Starts a libp2p host for this server node that can be dialed from any other open-chat node
func CreateFederationHost(
	port int,
) {
	var debug = true
	var r io.Reader
	if debug {
		r = mrand.New(mrand.NewSource(int64(port)))
	} else {
		r = rand.Reader
	}

	h, err := CreateHost(port, r, "private.key")
	fmt.Println("================", "Setting up Host Node", "================")
	fmt.Println("Host Identity:", h.ID())
	p2pAdress := fmt.Sprintf("%s/p2p/%s", h.Addrs()[0], h.ID())
	fmt.Println("P2P Address:", p2pAdress)
	if err != nil {
		log.Println(err)
		return
	}

	// Start the peer
	StartRequestReceivingPeer(context.Background(), h, IncomingRequestStreamHander)
	federation.FederationHost = h
}
