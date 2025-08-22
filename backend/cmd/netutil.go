package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// reserveServerListener binds host:port up front so that a busy port fails
// fast with an actionable message, before any expensive database, Redis or
// integration bootstrap work happens. The returned listener is later handed to
// http.Server.Serve.
func reserveServerListener(host string, port uint16) (net.Listener, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		return listener, nil
	}

	if version, ok := probeOpenChatVersion(host, port); ok {
		return nil, fmt.Errorf("a server is already running on %s (open-chat %s); stop it or use a different --port", addr, version)
	}
	return nil, fmt.Errorf("port %s is already in use by another process: %w", addr, err)
}

// probeOpenChatVersion performs a short GET /api/version against host:port and
// returns the reported version when the endpoint answers like Open Chat. It is
// used to distinguish "another open-chat is running" from a generic port
// conflict and by the install/status commands.
func probeOpenChatVersion(host string, port uint16) (string, bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://%s/api/version", net.JoinHostPort(probeDialHost(host), strconv.Itoa(int(port))))
	resp, err := client.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	var payload struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", false
	}
	version := strings.TrimSpace(payload.Version)
	if version == "" {
		return "", false
	}
	return version, true
}

// probeDialHost maps wildcard bind addresses to a dialable loopback address so
// that probing a server bound to 0.0.0.0/:: does not depend on platform
// specific handling of unspecified addresses.
func probeDialHost(host string) string {
	trimmed := strings.TrimSpace(strings.Trim(host, "[]"))
	switch trimmed {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "::":
		return "::1"
	default:
		return host
	}
}
