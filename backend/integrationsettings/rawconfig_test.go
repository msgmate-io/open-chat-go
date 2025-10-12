package integrationsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	goyaml "go.yaml.in/yaml/v3"
)

func rawConfigDefinition() integrationinterface.Definition {
	return integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_TOKEN", Sensitive: true},
			{Key: "OCI_DEMO_HOST"},
		},
		RuntimeConfigAliases: []integrationinterface.RuntimeConfigAlias{
			{JSONKey: "token", EnvKey: "OCI_DEMO_TOKEN"},
		},
	}
}

func TestRedactAndRestoreRoundTrip(t *testing.T) {
	def := rawConfigDefinition()
	values := map[string]runtimecfg.Value{
		"OCI_DEMO_TOKEN": {Value: "super-secret", Sensitive: true},
		"OCI_DEMO_HOST":  {Value: "example.com"},
	}
	root := map[string]interface{}{
		"env": map[string]interface{}{
			"OCI_DEMO_TOKEN": "super-secret",
			"OCI_DEMO_HOST":  "example.com",
		},
		"integrations": map[string]interface{}{
			"demo": map[string]interface{}{"token": "super-secret", "host": "example.com"},
		},
	}

	redacted := RedactConfig(root, values, []integrationinterface.Definition{def})
	env := redacted["env"].(map[string]interface{})
	if env["OCI_DEMO_TOKEN"] != RedactedValue {
		t.Fatalf("expected sensitive env redacted, got %#v", env["OCI_DEMO_TOKEN"])
	}
	if env["OCI_DEMO_HOST"] != "example.com" {
		t.Fatalf("expected non-sensitive env preserved, got %#v", env["OCI_DEMO_HOST"])
	}
	demo := redacted["integrations"].(map[string]interface{})["demo"].(map[string]interface{})
	if demo["token"] != RedactedValue {
		t.Fatalf("expected sensitive alias redacted, got %#v", demo["token"])
	}
	if demo["host"] != "example.com" {
		t.Fatalf("expected non-sensitive alias preserved, got %#v", demo["host"])
	}

	restored := RestoreRedactedConfig(root, redacted, values, []integrationinterface.Definition{def})
	restoredEnv := restored["env"].(map[string]interface{})
	if restoredEnv["OCI_DEMO_TOKEN"] != "super-secret" {
		t.Fatalf("expected restored env secret, got %#v", restoredEnv["OCI_DEMO_TOKEN"])
	}
	restoredDemo := restored["integrations"].(map[string]interface{})["demo"].(map[string]interface{})
	if restoredDemo["token"] != "super-secret" {
		t.Fatalf("expected restored alias secret, got %#v", restoredDemo["token"])
	}
}

func TestRestoreRedactedDropsMissingOriginal(t *testing.T) {
	def := rawConfigDefinition()
	values := map[string]runtimecfg.Value{
		"OCI_DEMO_TOKEN": {Value: "super-secret", Sensitive: true},
	}
	original := map[string]interface{}{
		"integrations": map[string]interface{}{"demo": map[string]interface{}{}},
	}
	incoming := map[string]interface{}{
		"integrations": map[string]interface{}{
			"demo": map[string]interface{}{"token": RedactedValue},
		},
	}

	restored := RestoreRedactedConfig(original, incoming, values, []integrationinterface.Definition{def})
	demo := restored["integrations"].(map[string]interface{})["demo"].(map[string]interface{})
	if _, ok := demo["token"]; ok {
		t.Fatalf("expected redacted key without original to be dropped, got %#v", demo)
	}
}

func TestValidateConfigDocument(t *testing.T) {
	defs := []integrationinterface.Definition{rawConfigDefinition()}

	if errs := ValidateConfigDocument(map[string]interface{}{
		"env":          map[string]interface{}{"OCI_DEMO_HOST": "h"},
		"integrations": map[string]interface{}{"demo": map[string]interface{}{"token": "t", "host": "h"}},
		"bootstrap":    map[string]interface{}{"git": map[string]interface{}{}},
	}, defs); len(errs) != 0 {
		t.Fatalf("expected valid document, got %v", errs)
	}

	cases := []struct {
		name string
		root map[string]interface{}
		want string
	}{
		{"unknown top-level", map[string]interface{}{"weird": 1}, "unknown top-level key"},
		{"unknown integration", map[string]interface{}{"integrations": map[string]interface{}{"nope": map[string]interface{}{"x": "y"}}}, "unknown integration"},
		{"unknown key", map[string]interface{}{"integrations": map[string]interface{}{"demo": map[string]interface{}{"nope": "y"}}}, "does not map to a declared runtime env var"},
		{"env not mapping", map[string]interface{}{"env": "nope"}, "env must be a mapping"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateConfigDocument(tc.root, defs)
			if len(errs) == 0 {
				t.Fatalf("expected validation error containing %q", tc.want)
			}
			found := false
			for _, err := range errs {
				if strings.Contains(err, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected error containing %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestLoadAndSaveConfigDocumentJSON(t *testing.T) {
	path := writeTempConfig(t, `{"env":{"OCI_DEMO_HOST":"h"},"custom":true}`)

	root, format, err := LoadConfigDocument(path)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}
	if format != "json" {
		t.Fatalf("expected json format, got %q", format)
	}
	root["env"].(map[string]interface{})["OCI_DEMO_HOST"] = "changed"

	savedFormat, err := SaveConfigDocument(path, root)
	if err != nil {
		t.Fatalf("SaveConfigDocument: %v", err)
	}
	if savedFormat != "json" {
		t.Fatalf("expected json format preserved, got %q", savedFormat)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, raw)
	}
	env := decoded["env"].(map[string]interface{})
	if env["OCI_DEMO_HOST"] != "changed" {
		t.Fatalf("expected updated host, got %#v", env)
	}
	if decoded["custom"] != true {
		t.Fatalf("expected unknown key preserved, got %#v", decoded["custom"])
	}
}

func TestLoadAndSaveConfigDocumentYAML(t *testing.T) {
	path := writeTempConfig(t, "env:\n  OCI_DEMO_HOST: h\n")

	root, format, err := LoadConfigDocument(path)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}
	if format != "yaml" {
		t.Fatalf("expected yaml format, got %q", format)
	}
	root["env"].(map[string]interface{})["OCI_DEMO_HOST"] = "changed"

	if _, err := SaveConfigDocument(path, root); err != nil {
		t.Fatalf("SaveConfigDocument: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var decoded map[string]interface{}
	if err := goyaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("expected YAML output: %v\n%s", err, raw)
	}
	env := decoded["env"].(map[string]interface{})
	if env["OCI_DEMO_HOST"] != "changed" {
		t.Fatalf("expected updated host, got %#v", env)
	}
}

func TestLoadConfigDocumentNotPersistable(t *testing.T) {
	if _, _, err := LoadConfigDocument(""); err == nil {
		t.Fatal("expected error for empty source")
	}
	if _, _, err := LoadConfigDocument("inline://abc"); err == nil {
		t.Fatal("expected error for inline source")
	}
	if _, _, err := LoadConfigDocument(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSaveConfigDocumentPreservesPermissions(t *testing.T) {
	path := writeTempConfig(t, "env:\n  OCI_DEMO_HOST: h\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	root, _, err := LoadConfigDocument(path)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}
	if _, err := SaveConfigDocument(path, root); err != nil {
		t.Fatalf("SaveConfigDocument: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("expected permissions preserved 0640, got %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".open-chat-config-") {
			t.Fatalf("expected no leftover temp files, found %s", entry.Name())
		}
	}
}

func TestBuildRuntimeConfigDocument(t *testing.T) {
	def := rawConfigDefinition()
	values := map[string]runtimecfg.Value{
		"OCI_DEMO_TOKEN": {Value: "secret", Sensitive: true},
		"OCI_DEMO_HOST":  {Value: "example.com"},
		"PORT":           {Value: "1984"},
	}
	root := BuildRuntimeConfigDocument([]integrationinterface.Definition{def}, values)

	env := root["env"].(map[string]interface{})
	if env["PORT"] != "1984" {
		t.Fatalf("expected env PORT, got %#v", env)
	}
	if _, ok := env["OCI_DEMO_TOKEN"]; ok {
		t.Fatalf("expected alias env key moved to integrations, got %#v", env)
	}
	demo := root["integrations"].(map[string]interface{})["demo"].(map[string]interface{})
	if demo["token"] != "secret" {
		t.Fatalf("expected alias token in integrations, got %#v", demo)
	}
}
