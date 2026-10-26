package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// registrySources are the model-registry files the free-model set is derived
// from. They are read relative to the backend module root, mirroring how
// database/model_config.go embeds and seeds the same files.
var registrySources = []string{
	"database/data/default_model_configs.json",
	"database/data/extra_default_models_config.json",
}

// entryConfiguration is the registry entry shape used by the seeding code
// (see modelConfigFileEntry in database/model_config.go). Only the fields the
// runner needs are defined; the rest pass through untouched.
type entryConfiguration struct {
	Model   string `json:"model"`
	Backend string `json:"backend"`
	// The `tier` field is a registry-level hint (e.g. "free" / "paid") parsed
	// for documentation only; the authoritative free marker is the ":free"
	// suffix OpenRouter assigns to free-tier model identifiers.
	Tier string `json:"tier"`
}

type registryEntry struct {
	Title         string             `json:"title"`
	Configuration entryConfiguration `json:"configuration"`
}

// LoadFreeModels returns the deduplicated set of free models found in the
// registry files, in registry order. A model is considered free when its
// configuration model identifier ends with the OpenRouter free-tier marker
// ":free". The list is always read from the registry on disk so the sweep
// never hardcodes (or guesses) the set, and it never calls a live API.
func LoadFreeModels(backendRoot string) ([]FreeModel, error) {
	seen := map[string]bool{}
	var out []FreeModel
	for _, rel := range registrySources {
		path := filepath.Join(backendRoot, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("benchmark: read registry %s: %w", rel, err)
		}
		var entries []registryEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("benchmark: parse registry %s: %w", rel, err)
		}
		for _, e := range entries {
			raw := strings.ToLower(e.Configuration.Model)
			if !strings.HasSuffix(raw, ":free") {
				continue
			}
			id := e.Configuration.Model
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, FreeModel{ID: id, Title: e.Title, Source: rel})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("benchmark: no :free models found in %v", registrySources)
	}
	return out, nil
}

// SourceRegistryNames returns the registry names for artifact provenance.
func SourceRegistryNames() []string {
	return append([]string(nil), registrySources...)
}
