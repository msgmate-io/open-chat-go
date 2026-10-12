package integrations

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestCoreOnlyIntegrationNamesMatchManifest guards the hand-maintained core-only
// set against drift from integrations.yaml.
func TestCoreOnlyIntegrationNamesMatchManifest(t *testing.T) {
	path := filepath.Join("..", "..", "integrations.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("manifest not available: %v", err)
	}

	var manifest struct {
		Profiles map[string][]string `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}

	coreIDs, ok := manifest.Profiles["core-only"]
	if !ok {
		t.Fatalf("integrations.yaml has no core-only profile")
	}

	fromManifest := map[string]struct{}{}
	for _, id := range coreIDs {
		if id == "*" {
			t.Skip("core-only uses the wildcard profile; skipping exact match")
		}
		fromManifest[id] = struct{}{}
	}

	for id := range fromManifest {
		if _, ok := coreOnlyIntegrationNames[id]; !ok {
			t.Errorf("core-only profile contains %q but coreOnlyIntegrationNames does not", id)
		}
	}
	for id := range coreOnlyIntegrationNames {
		if _, ok := fromManifest[id]; !ok {
			t.Errorf("coreOnlyIntegrationNames contains %q but the core-only profile does not", id)
		}
	}
}
