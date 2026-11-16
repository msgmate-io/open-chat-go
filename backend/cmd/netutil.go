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

// EnsureLocalServer decides whether a caller that wants to serve host:port can
// bind it or has to attach to an already running Open Chat server on that
// device. It exists so multi-session frontends (`open-chat code` in several
// shells, `open-chat run`) stop failing on a local port that is held by our own
// server: when probeOpenChatVersion answers, the listener belongs to Open Chat
// and is reusable, so reused=true is returned and err is nil.
//
// When nothing reusable answers, the helper verifies that the port is bindable
// (and releases the probe listener again, reporting reused=false) so a busy or
// unresolvable port still fails fast with the actionable wording produced by
// reserveServerListener ("a server is already running on …" or "port … is
// already in use by another process"). Callers that own the process should then
// call reserveServerListener to obtain the listener they serve with.
func EnsureLocalServer(host string, port uint16) (reused bool, err error) {
	if _, ok := probeOpenChatVersion(host, port); ok {
		// The listener is our own server; a second session must reuse it
		// instead of failing on the busy port.
		return true, nil
	}

	listener, err := reserveServerListener(host, port)
	if err != nil {
		return false, err
	}
	// Free port: nothing to reuse. Release the probe reservation so the caller
	// can bind it itself (small, documented race window).
	_ = listener.Close()
	return false, nil
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
