package integrationsettings

import (
	"errors"
	"strings"
	"testing"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func testDefinition() integrationinterface.Definition {
	return integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_ENABLED"},
			{Key: "OCI_DEMO_PORT"},
			{Key: "OCI_DEMO_HOST"},
		},
	}
}

func strPtr(v string) *string { return &v }

func TestValidateValuesRejectsUnknownKey(t *testing.T) {
	_, err := ValidateValues(testDefinition(), map[string]*string{"OCI_NOPE": strPtr("x")})
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

func TestValidateValuesCoercesBool(t *testing.T) {
	out, err := ValidateValues(testDefinition(), map[string]*string{"OCI_DEMO_ENABLED": strPtr("1")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := *out["OCI_DEMO_ENABLED"]; got != "true" {
		t.Fatalf("expected normalized bool true, got %q", got)
	}
}

func TestValidateValuesRejectsInvalidBool(t *testing.T) {
	if _, err := ValidateValues(testDefinition(), map[string]*string{"OCI_DEMO_ENABLED": strPtr("maybe")}); err == nil {
		t.Fatal("expected error for invalid bool")
	}
}

func TestValidateValuesEnforcesNumberRange(t *testing.T) {
	min := 1.0
	max := 10.0
	def := integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_RATE", Type: "number", Min: &min, Max: &max},
		},
	}
	if _, err := ValidateValues(def, map[string]*string{"OCI_DEMO_RATE": strPtr("20")}); err == nil {
		t.Fatal("expected range rejection above max")
	}
	if _, err := ValidateValues(def, map[string]*string{"OCI_DEMO_RATE": strPtr("0")}); err == nil {
		t.Fatal("expected range rejection below min")
	}
	out, err := ValidateValues(def, map[string]*string{"OCI_DEMO_RATE": strPtr("5")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := *out["OCI_DEMO_RATE"]; got != "5" {
		t.Fatalf("expected value preserved, got %q", got)
	}
}

func TestValidateValuesEnforcesSelectOptions(t *testing.T) {
	def := integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_MODE", Type: "select", Options: []string{"a", "b"}},
		},
	}
	if _, err := ValidateValues(def, map[string]*string{"OCI_DEMO_MODE": strPtr("c")}); err == nil {
		t.Fatal("expected option rejection")
	}
	if _, err := ValidateValues(def, map[string]*string{"OCI_DEMO_MODE": strPtr("b")}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateValuesEnforcesRequired(t *testing.T) {
	def := integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_HOST", Required: true},
		},
	}
	if _, err := ValidateValues(def, map[string]*string{"OCI_DEMO_HOST": nil}); err == nil {
		t.Fatal("expected required rejection for unset")
	}
	if _, err := ValidateValues(def, map[string]*string{"OCI_DEMO_HOST": strPtr("  ")}); err == nil {
		t.Fatal("expected required rejection for empty")
	}
}

func TestValidateValuesAllowsUnset(t *testing.T) {
	out, err := ValidateValues(testDefinition(), map[string]*string{"OCI_DEMO_HOST": nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["OCI_DEMO_HOST"] != nil {
		t.Fatal("expected nil to mean unset")
	}
}

// TestPersistValuesWithRemoteInlineSource covers the per-integration settings
// path: when the local config source is inline (not persistable) the merged
// document must still be mirrored to the remote backing store.
func TestPersistValuesWithRemoteInlineSource(t *testing.T) {
	prevSource := runtimecfg.GetConfigSource()
	prevValues := runtimecfg.GetAll()
	t.Cleanup(func() {
		runtimecfg.SetConfigSource(prevSource)
		runtimecfg.SetAll(prevValues)
		RegisterRemoteConfigPersister(nil)
	})
	runtimecfg.SetConfigSource("inline --config YAML")
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"OCI_DEMO_HOST": {Value: "old-host"},
	})

	var mirrored []byte
	RegisterRemoteConfigPersister(func(data []byte) (string, error) {
		mirrored = append([]byte(nil), data...)
		return "k8s-secret/demo", nil
	})

	outcome := PersistValuesWithRemote(testDefinition(), map[string]*string{"OCI_DEMO_HOST": strPtr("new-host")})
	if !outcome.RemotePersisted || outcome.RemoteTarget != "k8s-secret/demo" {
		t.Fatalf("expected remote persist, got %#v", outcome)
	}
	if !outcome.Persisted {
		t.Fatal("expected persisted when the remote mirror succeeds")
	}
	if !strings.Contains(string(mirrored), "new-host") {
		t.Fatalf("expected merged document mirrored, got %s", mirrored)
	}
}
