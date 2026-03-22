package workqueue

// DefaultBackendHost is the backend base URL async bot tasks fall back to when
// the resolved backend host is empty. It is deliberately the *same* constant the
// worker's `--backend-host` flag (OPEN_CHAT_HOST) defaults to, so the flag
// default and the task-level fallback cannot drift apart again — the literal
// used to be duplicated in `cmd/worker.go` and in the bot reply task, which
// meant changing one silently left the other behind.
//
// It is a last-resort guard, not a configuration path: it only applies when an
// operator explicitly sets OPEN_CHAT_HOST to an empty value (or clears the flag
// default), so it never overrides a configured host.
const DefaultBackendHost = "http://127.0.0.1:1984"

// BackendHostOrDefault resolves the backend base URL a task should talk to: the
// configured host when it is non-empty, otherwise DefaultBackendHost.
func BackendHostOrDefault(host string) string {
	if host != "" {
		return host
	}
	return DefaultBackendHost
}
