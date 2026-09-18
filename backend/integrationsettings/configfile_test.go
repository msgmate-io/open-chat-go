package integrationsettings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func configTestDefinition() integrationinterface.Definition {
	return integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_TOKEN"},
			{Key: "OCI_DEMO_HOST"},
			{Key: "OCI_DEMO_ENABLED"},
		},
		RuntimeConfigAliases: []integrationinterface.RuntimeConfigAlias{
			{JSONKey: "token", EnvKey: "OCI_DEMO_TOKEN"},
		},
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "open-chat.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func readJSONMap(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return out
}

func TestMergeValuesWritesAliasAndEnv(t *testing.T) {
	path := writeTempConfig(t, `{
  "custom": {"keep": true},
  "env": {"OCI_DEMO_HOST": "old.example.com"},
  "integrations": {"demo": {"token": "old-token"}}
}`)
	def := configTestDefinition()

	err := MergeValues(path, def, map[string]*string{
		"OCI_DEMO_TOKEN": strPtr("new-token"),
		"OCI_DEMO_HOST":  strPtr("new.example.com"),
	})
	if err != nil {
		t.Fatalf("MergeValues: %v", err)
	}

	root := readJSONMap(t, path)
	if _, ok := root["custom"]; !ok {
		t.Fatal("expected unknown top-level key preserved")
	}
	env := root["env"].(map[string]interface{})
	if env["OCI_DEMO_HOST"] != "new.example.com" {
		t.Fatalf("expected env host updated, got %v", env["OCI_DEMO_HOST"])
	}
	integrations := root["integrations"].(map[string]interface{})
	demo := integrations["demo"].(map[string]interface{})
	if demo["token"] != "new-token" {
		t.Fatalf("expected alias token updated, got %v", demo["token"])
	}
	if _, exists := env["OCI_DEMO_TOKEN"]; exists {
		t.Fatal("expected env token removed in favor of alias")
	}
}

func TestMergeValuesAliasBoolIsTyped(t *testing.T) {
	def := configTestDefinition()
	def.RuntimeConfigAliases = append(def.RuntimeConfigAliases, integrationinterface.RuntimeConfigAlias{
		JSONKey: "enabled",
		EnvKey:  "OCI_DEMO_ENABLED",
	})
	path := writeTempConfig(t, `{}`)
	if err := MergeValues(path, def, map[string]*string{
		"OCI_DEMO_ENABLED": strPtr("true"),
	}); err != nil {
		t.Fatalf("MergeValues: %v", err)
	}
	root := readJSONMap(t, path)
	integrations := root["integrations"].(map[string]interface{})
	demo := integrations["demo"].(map[string]interface{})
	if value, ok := demo["enabled"].(bool); !ok || !value {
		t.Fatalf("expected typed bool true, got %#v", demo["enabled"])
	}
}

func TestMergeValuesUnset(t *testing.T) {
	path := writeTempConfig(t, `{
  "env": {"OCI_DEMO_HOST": "old.example.com"},
  "integrations": {"demo": {"token": "old-token"}}
}`)
	if err := MergeValues(path, configTestDefinition(), map[string]*string{
		"OCI_DEMO_HOST":  nil,
		"OCI_DEMO_TOKEN": nil,
	}); err != nil {
		t.Fatalf("MergeValues: %v", err)
	}
	root := readJSONMap(t, path)
	if _, ok := root["env"]; ok {
		t.Fatalf("expected env section removed, got %#v", root["env"])
	}
	if _, ok := root["integrations"]; ok {
		t.Fatalf("expected integrations section removed, got %#v", root["integrations"])
	}
}

func TestMergeValuesPreservesPermissions(t *testing.T) {
	path := writeTempConfig(t, `{}`)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := MergeValues(path, configTestDefinition(), map[string]*string{
		"OCI_DEMO_HOST": strPtr("h"),
	}); err != nil {
		t.Fatalf("MergeValues: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("expected permissions preserved 0640, got %o", info.Mode().Perm())
	}
}

func TestMergeValuesRejectsNonJSON(t *testing.T) {
	path := writeTempConfig(t, "env:\n  OCI_DEMO_HOST: example.com\n")
	err := MergeValues(path, configTestDefinition(), map[string]*string{"OCI_DEMO_HOST": strPtr("x")})
	if !errors.Is(err, ErrNotPersistable) {
		t.Fatalf("expected ErrNotPersistable, got %v", err)
	}
}

func TestMergeValuesRejectsMissingFile(t *testing.T) {
	err := MergeValues(filepath.Join(t.TempDir(), "missing.json"), configTestDefinition(), map[string]*string{"OCI_DEMO_HOST": strPtr("x")})
	if !errors.Is(err, ErrNotPersistable) {
		t.Fatalf("expected ErrNotPersistable, got %v", err)
	}
}
