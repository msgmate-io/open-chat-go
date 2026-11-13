package cmd

import (
	"net"
	"net/http"
	"strconv"
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

func TestEnsureLocalServerReusesRunningOpenChat(t *testing.T) {
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

	// ci#1070: a second `open-chat code` in another shell must attach to the
	// server the first session started instead of dying on the busy port.
	reused, err := EnsureLocalServer("127.0.0.1", port)
	if err != nil {
		t.Fatalf("expected the running open-chat server to be reusable, got: %v", err)
	}
	if !reused {
		t.Fatal("expected reused=true when an open-chat server answers on the port")
	}
}

func TestEnsureLocalServerRejectsForeignListener(t *testing.T) {
	ln, port := listenLoopback(t)
	srv := &http.Server{Handler: http.HandlerFunc(http.NotFound)}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	reused, err := EnsureLocalServer("127.0.0.1", port)
	if err == nil {
		t.Fatal("expected an error when the port is held by a foreign process")
	}
	if reused {
		t.Fatal("expected reused=false when the listener is not an open-chat server")
	}
	if !strings.Contains(err.Error(), "already in use by another process") {
		t.Fatalf("expected the actionable foreign-process wording, got: %v", err)
	}
}

func TestEnsureLocalServerReservesFreePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on loopback: %v", err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()
	defer ln.Close()

	reused, err := EnsureLocalServer("127.0.0.1", port)
	if err != nil {
		t.Fatalf("expected a free port to be bindable, got: %v", err)
	}
	if reused {
		t.Fatal("expected reused=false on a free port")
	}
	// The helper must not keep the port reserved: the caller binds it itself.
	ln2, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(int(port)))
	if err != nil {
		t.Fatalf("expected the probed port to be free again, got: %v", err)
	}
	_ = ln2.Close()
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
