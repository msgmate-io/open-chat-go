package integrationsettings

import (
	"testing"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func TestListSnapshotsFiltersEmptyIntegrations(t *testing.T) {
	defs := []integrationinterface.Definition{
		{Name: "with_vars", RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{{Key: "OCI_WITH_VARS_A"}}},
		{Name: "empty"},
		{Name: "another", RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{{Key: "OCI_ANOTHER_A"}}},
	}
	values := map[string]runtimecfg.Value{
		"OCI_WITH_VARS_A": {Value: "x"},
	}
	snapshots := ListSnapshots(defs, values, false)
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].Name != "another" || snapshots[1].Name != "with_vars" {
		t.Fatalf("expected alphabetical order, got %q, %q", snapshots[0].Name, snapshots[1].Name)
	}
}

func TestFindDefinitionCaseInsensitive(t *testing.T) {
	defs := []integrationinterface.Definition{{Name: "Demo"}}
	def, ok := FindDefinition(defs, "demo")
	if !ok || def.Name != "Demo" {
		t.Fatalf("expected to find Demo, got %q ok=%v", def.Name, ok)
	}
	if _, ok := FindDefinition(defs, "missing"); ok {
		t.Fatal("expected missing definition to not be found")
	}
}
