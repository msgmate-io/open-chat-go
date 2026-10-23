package workqueue

import "testing"

// TestBackendHostOrDefault pins the single source of truth for the backend base
// URL used by async bot tasks: a configured host wins verbatim, an empty host
// resolves to DefaultBackendHost. Both the worker's `--backend-host` flag default
// and the bot reply task's fallback read DefaultBackendHost, so this test is what
// keeps the two from drifting apart again.
func TestBackendHostOrDefault(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
	}{
		{name: "configured host is kept verbatim", host: "https://chat.example.com", want: "https://chat.example.com"},
		{name: "configured host without trailing slash is untouched", host: "http://127.0.0.1:9000", want: "http://127.0.0.1:9000"},
		{name: "empty host falls back to the default", host: "", want: DefaultBackendHost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BackendHostOrDefault(tc.host); got != tc.want {
				t.Fatalf("BackendHostOrDefault(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

// TestDefaultBackendHostNeverEmpty guards the fallback itself: an empty default
// would make the bot reply task build a client against an empty base URL.
func TestDefaultBackendHostNeverEmpty(t *testing.T) {
	if DefaultBackendHost == "" {
		t.Fatal("DefaultBackendHost must not be empty")
	}
	if got := BackendHostOrDefault(""); got == "" {
		t.Fatal("BackendHostOrDefault(\"\") must never return an empty host")
	}
}
