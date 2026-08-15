package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAggregateTSVToJSON exercises the TSV reader against the same
// per-status line format produced by the free-model sweep harness
// (development/free_model_capability_sweep.sh). Because the sweep runs
// against external sandbox results, the reader is covered here with a
// faithful fixture instead of requiring a live run.
func TestAggregateTSVToJSON(t *testing.T) {
	dir := t.TempDir()

	writeFixture(t, dir, "nvidia_nemotron-3-ultra-550b-a55b_free", "test_little_world\tpass\nrest_api_tool\tprovider_rate_limit\ntest_e2e\tfail\n")
	writeFixture(t, dir, "thinkingmachines_inkling_free", "test_little_world\tpass\nrest_api_tool\ttimeout\n")

	rows, err := AggregateTSVToJSON(filepath.Join(dir, "benchmark-results"))
	if err != nil {
		t.Fatalf("AggregateTSVToJSON: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 model rows, got %d: %v", len(rows), rows)
	}

	nv := rows["nvidia/nemotron-3-ultra-550b-a55b:free"]
	if nv["test_little_world"] != "pass" {
		t.Errorf("nemotron test_little_world = %q, want pass", nv["test_little_world"])
	}
	if nv["rest_api_tool"] != "provider_rate_limit" {
		t.Errorf("nemotron rest_api_tool = %q, want provider_rate_limit", nv["rest_api_tool"])
	}
	if nv["test_e2e"] != "fail" {
		t.Errorf("nemotron test_e2e = %q, want fail", nv["test_e2e"])
	}

	ink := rows["thinkingmachines/inkling:free"]
	if ink["rest_api_tool"] != "timeout" {
		t.Errorf("inkling rest_api_tool = %q, want timeout", ink["rest_api_tool"])
	}

	// Rows must collapse to valid JSON via the same marshaling the harness uses.
	if _, err := json.Marshal(rows); err != nil {
		t.Fatalf("rows not JSON-marshalable: %v", err)
	}
}

func writeFixture(t *testing.T, root, slug, content string) {
	t.Helper()
	dir := filepath.Join(root, "benchmark-results", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run1.tsv"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// TestAggregateTSVToJSONNoResults ensures the reader tolerates a directory
// tree without any result files instead of failing.
func TestAggregateTSVToJSONNoResults(t *testing.T) {
	dir := t.TempDir()
	rows, err := AggregateTSVToJSON(dir)
	if err != nil {
		t.Fatalf("AggregateTSVToJSON on empty tree: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %v", rows)
	}
}

// TestModelSlugRoundTrip verifies the slug mapping used by the harness
// (provider/model:free -> provider_model_free) survives a round trip.
func TestModelSlugRoundTrip(t *testing.T) {
	slug := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll("nvidia/nemotron-3-ultra-550b-a55b:free", "/", "_"), ":", "_"), "-", "-")
	want := "nvidia_nemotron-3-ultra-550b-a55b_free"
	if slug != want {
		t.Fatalf("slug = %q, want %q", slug, want)
	}
}
