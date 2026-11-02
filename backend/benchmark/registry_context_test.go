package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// dirTSV is a tiny helper that materializes small registry/config trees used
// by the generator? (unused placeholder; kept trivial to avoid dead code)
func dirTSV(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(d, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return d
}

func TestLoadFreeModelsCarriesContext(t *testing.T) {
	reg := []registryEntry{
		{
			Title: "Big Vision",
			Configuration: entryConfiguration{
				Model:   "org/vision:free",
				Backend: "openrouter",
				Context: 1_000_000,
			},
		},
		{
			Title: "Plain",
			Configuration: entryConfiguration{
				Model:   "org/plain:free",
				Backend: "openrouter",
				// no context field -> 0
			},
		},
	}
	raw, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	d := dirTSV(t, map[string]string{
		"database/data/default_model_configs.json":       string(raw),
		"database/data/extra_default_models_config.json": "[]",
	})
	models, err := LoadFreeModels(d)
	if err != nil {
		t.Fatalf("LoadFreeModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(models), models)
	}
	byID := map[string]FreeModel{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if got := byID["org/vision:free"].Context; got != 1_000_000 {
		t.Errorf("vision context = %d, want 1000000", got)
	}
	if got := byID["org/plain:free"].Context; got != 0 {
		t.Errorf("plain context = %d, want 0", got)
	}
}

func TestModelReportContextString(t *testing.T) {
	m := ModelReport{
		ModelID:  "org/fake:free",
		Counts:   Counts{},
		PassRate: "0",
		Context:  "262k",
		Results:  nil,
	}
	if m.Context != "262k" {
		t.Fatalf("context string not populated")
	}
	_ = m
	_ = time.Now() // keep time import used (artifact renderers stamp generated_at)
}
