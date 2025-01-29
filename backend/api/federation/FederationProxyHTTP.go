package federation

import (
	"bufio"
	"fmt"
	"github.com/libp2p/go-libp2p/core/network"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// CreateIncomingRequestStreamHandler creates a stream handler with configured host and port
func (h *FederationHandler) CreateIncomingRequestStreamHandler(host string, hostPort int, pathPrefixWhitelist []string, restrictToNetworkName string) network.StreamHandler {
	// empty whitelist means allow all
	preprocessor := func(path string) bool {
		return true
	}
	if len(pathPrefixWhitelist) > 0 {
		preprocessor = func(path string) bool {
			// check if the path is in the whitelist
			for _, prefix := range pathPrefixWhitelist {
				if strings.HasPrefix(path, prefix) {
					return true
				}
			}
			return false
		}
	}
	// check possible proxies
	return func(stream network.Stream) {
		defer stream.Close()

		buf := bufio.NewReader(stream)
		req, err := http.ReadRequest(buf)
		if err != nil {
			stream.Reset()
			log.Println(err)
			return
		}

		if restrictToNetworkName != "" {
			// then the requesting peer must be in the network!
			if !h.NetworkPeerIds[restrictToNetworkName][stream.Conn().RemotePeer().String()] {
				log.Println("Peer not in network!", stream.Conn().RemotePeer().String(), h.NetworkPeerIds[restrictToNetworkName])
				stream.Reset()
				return
			}
		}

		if !preprocessor(req.URL.Path) {
			stream.Reset()
			log.Println("Request not allowed", req.URL.Path)
			return
		}

		fmt.Println("Handling Incoming Request:", req.URL.Path, "from peer", stream.Conn().RemotePeer().String())

		defer req.Body.Close()

		fullHost := fmt.Sprintf("%s:%d", host, hostPort)
		req.URL.Scheme = "http"
		req.URL.Host = fullHost

		outreq := new(http.Request)
		*outreq = *req

		outreq.Header = req.Header
		resp, err := http.DefaultTransport.RoundTrip(outreq)
		if err != nil {
			stream.Reset()
			log.Println("Error: handling incoming request", err)
			return
		}

		fmt.Println("Response:", resp, resp.Header)

		// Check if the response is an octet stream
		if false && resp.Header.Get("Content-Type") == "application/octet-stream" {
			// Create a temporary file
			tmpFile, err := os.CreateTemp("", "octet-stream-*")
			if err != nil {
				log.Println("Error creating temp file:", err)
				stream.Reset()
				return
			}
			defer tmpFile.Close()

			// Save the response body to the temporary file
			if _, err := io.Copy(tmpFile, resp.Body); err != nil {
				log.Println("Error saving octet stream to file:", err)
				stream.Reset()
				return
			}

			log.Println("Octet stream saved to:", tmpFile.Name())

			// Reset the response body reader for further processing
			tmpFile.Seek(0, io.SeekStart) // Reset file pointer to the beginning
			resp.Body = io.NopCloser(tmpFile)

			// Update Content-Length header
			fileInfo, err := tmpFile.Stat()
			if err == nil {
				resp.Header.Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))
			}
		}

		/**
		writer := bufio.NewWriter(stream)
		err = resp.Write(writer)
		if err != nil {
			stream.Reset()
			log.Println("Error writing response:", err)
			return
		}

		err = writer.Flush()
		if err != nil {
			stream.Reset()
			log.Println("Error flushing writer:", err)
			return
		}

		// Ensure the response is fully read and handled
		if _, err := io.Copy(writer, resp.Body); err != nil {
			log.Println("Error copying response:", err)
		}

		defer resp.Body.Close()
		**/

		// resp.Write writes whatever response we obtained for our
		// request back to the stream.
		resp.Write(stream)

	}
}
