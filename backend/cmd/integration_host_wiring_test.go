package cmd

import (
	"strings"
	"testing"
)

// ci#423: the Host adapter (backend/integrationhost) is what lets compiled-in
// integrations resolve config/DB/logging through integrationinterface instead of
// importing backend/... directly. RequireHost panics when no adapter is
// installed, so *every* boot path that loads the compiled integrations must
// install the adapter before integration code can run. The worker boot path runs
// integration queue handlers and registers their models/migrations, so it needs
// the adapter exactly like the server does; installing it only in the server is
// how a worker would panic on the first integration task.
func TestBootPathsInstallIntegrationHost(t *testing.T) {
	for _, name := range bootSourceFiles {
		boot := readBootSource(t, name)
		if !strings.Contains(boot, "integrations.EnsureLoaded()") {
			t.Fatalf("backend/cmd/%s no longer loads the compiled integrations; keep bootSourceFiles in sync (ci#423)", name)
		}
		if !strings.Contains(boot, "integrationhost.Install()") {
			t.Errorf("backend/cmd/%s loads integrations but never installs the Host adapter: integrationinterface.RequireHost panics in this process (ci#423)", name)
		}
	}
}
