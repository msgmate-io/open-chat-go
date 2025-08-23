package cmd

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

func listenLoopback(t *testing.T) (net.Listener, uint16) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on loopback: %v", err)
	}
	return ln, uint16(ln.Addr().(*net.TCPAddr).Port)
}

func TestReserveServerListenerReportsOpenChat(t *testing.T) {
	ln, port := listenLoopback(t)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.2.3"}`))
			return
		}
		http.NotFound(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	_, err := reserveServerListener("127.0.0.1", port)
	if err == nil {
		t.Fatal("expected an error when the port is already serving open-chat")
	}
	msg := err.Error()
	if !strings.Contains(msg, "already running") || !strings.Contains(msg, "1.2.3") {
		t.Fatalf("expected an actionable already-running error mentioning the version, got: %v", err)
	}
}

func TestReserveServerListenerReportsForeignProcess(t *testing.T) {
	ln, port := listenLoopback(t)
	srv := &http.Server{Handler: http.HandlerFunc(http.NotFound)}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	_, err := reserveServerListener("127.0.0.1", port)
	if err == nil {
		t.Fatal("expected an error when the port is in use by another process")
	}
	if !strings.Contains(err.Error(), "already in use by another process") {
		t.Fatalf("expected a generic in-use error, got: %v", err)
	}
}

func TestReserveServerListenerSuccess(t *testing.T) {
	ln, err := reserveServerListener("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("expected to reserve a free port: %v", err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close reserved listener: %v", err)
	}

	ln2, err := reserveServerListener("127.0.0.1", port)
	if err != nil {
		t.Fatalf("expected to re-reserve the released port: %v", err)
	}
	defer ln2.Close()
}

func TestProbeDialHostMapsWildcards(t *testing.T) {
	cases := map[string]string{
		"":          "127.0.0.1",
		"0.0.0.0":   "127.0.0.1",
		"::":        "::1",
		"localhost": "localhost",
	}
	for input, want := range cases {
		if got := probeDialHost(input); got != want {
			t.Fatalf("probeDialHost(%q) = %q, want %q", input, got, want)
		}
	}
}
