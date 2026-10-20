package logging

import (
	"os"
	"strings"
	"testing"
)

// ci#645 ("reduce log heaviness") has two halves: the backend must honour a
// configurable level, and the ci deployment must actually be able to set it.
// The first half is covered by the level tests above; this guards the second.
//
// backend/main.go installs ConfigureFromEnv before any command runs, but that
// only helps if the ci profile passes LOG_LEVEL into the container. Without
// this wiring the "quiet down ci.msgmate.io" part of the ticket is impossible
// to switch on from the deployment side, exactly like the cutover env pinned
// by backend/nativecoder/cutoverenv_test.go.
func TestCiDeploymentPassesLogLevelToBackend(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.ci.yaml")
	if err != nil {
		t.Fatalf("read ci deployment profile: %v", err)
	}
	profile := string(raw)
	for _, key := range []string{EnvLogLevel, EnvLogFormat} {
		if key == "" {
			t.Fatalf("log env key must not be empty")
		}
		// Require the pass-through form "${KEY:-default}" so the deployment
		// keeps a sane default while remaining overridable.
		if !strings.Contains(profile, "${"+key+":-") {
			t.Errorf("docker-compose.ci.yaml must pass %s through to the backend service", key)
		}
	}
}
