package integrationsettings

import (
	"encoding/json"
	"errors"
	"testing"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func TestCoreDefinitionIncludesEnvAndBootstrap(t *testing.T) {
	prev := runtimecfg.GetAll()
	t.Cleanup(func() { runtimecfg.SetAll(prev) })
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"PORT":               {Value: "1984"},
		"OPENROUTER_API_KEY": {Value: "secret", Sensitive: true},
	})

	def := CoreDefinition()
	if def.Name != CoreSettingsName {
		t.Fatalf("expected core definition, got %q", def.Name)
	}
	if !def.AdminOnly {
		t.Fatal("expected core definition to be admin only")
	}

	descriptors := BuildDescriptors(def, runtimecfg.GetAll(), false)
	byKey := map[string]FieldDescriptor{}
	for _, field := range descriptors {
		byKey[field.Key] = field
	}

	port, ok := byKey["PORT"]
	if !ok {
		t.Fatal("expected PORT descriptor in core definition")
	}
	if port.Group != "Environment" || port.ConfigTarget != "env.PORT" {
		t.Fatalf("unexpected PORT descriptor: %#v", port)
	}

	token, ok := byKey["OPENROUTER_API_KEY"]
	if !ok {
		t.Fatal("expected OPENROUTER_API_KEY descriptor")
	}
	if !token.Sensitive || token.Type != FieldTypeSecret {
		t.Fatalf("expected sensitive secret field, got %#v", token)
	}

	git, ok := byKey["BOOTSTRAP_GIT"]
	if !ok {
		t.Fatal("expected BOOTSTRAP_GIT descriptor")
	}
	if git.Group != "Bootstrap" {
		t.Fatalf("expected bootstrap group, got %q", git.Group)
	}
	if git.ConfigTarget != "bootstrap.git" {
		t.Fatalf("expected bootstrap.git target, got %q", git.ConfigTarget)
	}
	if git.Type != FieldTypeJSON {
		t.Fatalf("expected json bootstrap field, got %q", git.Type)
	}

	for _, key := range []string{"BOOTSTRAP_USERS", "BOOTSTRAP_BOTS", "BOOTSTRAP_SSH", "BOOTSTRAP_OPENCODE", "BOOTSTRAP_MCP"} {
		if _, ok := byKey[key]; !ok {
			t.Fatalf("expected %s descriptor", key)
		}
	}
}

func TestLoadCoreBootstrapValuesFromJSON(t *testing.T) {
	path := writeTempConfig(t, `{
  "bootstrap": {
    "users": {"username": "admin"},
    "git": {"owner": "admin", "repositories": [{"name": "demo"}]}
  }
}`)

	values := LoadCoreBootstrapValues(path)
	if _, ok := values["BOOTSTRAP_MCP"]; ok {
		t.Fatal("did not expect BOOTSTRAP_MCP when section is absent")
	}
	var users map[string]interface{}
	if err := json.Unmarshal([]byte(values["BOOTSTRAP_USERS"]), &users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if users["username"] != "admin" {
		t.Fatalf("expected users username admin, got %#v", users)
	}
	var git map[string]interface{}
	if err := json.Unmarshal([]byte(values["BOOTSTRAP_GIT"]), &git); err != nil {
		t.Fatalf("decode git: %v", err)
	}
	if git["owner"] != "admin" {
		t.Fatalf("expected git owner admin, got %#v", git)
	}
}

func TestLoadCoreBootstrapValuesFromYAML(t *testing.T) {
	path := writeTempConfig(t, "bootstrap:\n  ssh:\n    owners:\n      - admin\n    keys:\n      - name: demo\n")

	values := LoadCoreBootstrapValues(path)
	raw, ok := values["BOOTSTRAP_SSH"]
	if !ok {
		t.Fatal("expected BOOTSTRAP_SSH from YAML config")
	}
	var ssh map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &ssh); err != nil {
		t.Fatalf("decode ssh: %v", err)
	}
	owners, ok := ssh["owners"].([]interface{})
	if !ok || len(owners) != 1 || owners[0] != "admin" {
		t.Fatalf("expected ssh owners [admin], got %#v", ssh["owners"])
	}
}

func TestLoadCoreBootstrapValuesInlineSynthesizes(t *testing.T) {
	prev := runtimecfg.GetOpenChatBootstrap()
	t.Cleanup(func() { runtimecfg.SetOpenChatBootstrap(prev) })
	runtimecfg.SetOpenChatBootstrap(runtimecfg.OpenChatBootstrap{
		UserSpecs:        []string{`{"username":"admin"}`},
		GitDefaultOwners: []string{"admin"},
	})

	values := LoadCoreBootstrapValues("inline --config YAML")
	if _, ok := values["BOOTSTRAP_USERS"]; !ok {
		t.Fatal("expected synthesized BOOTSTRAP_USERS")
	}
	raw, ok := values["BOOTSTRAP_GIT"]
	if !ok {
		t.Fatal("expected synthesized BOOTSTRAP_GIT")
	}
	var git map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &git); err != nil {
		t.Fatalf("decode git: %v", err)
	}
	if git["owners"] == nil {
		t.Fatalf("expected git owners in synthesized value, got %s", raw)
	}
}

func TestMergeValuesCoreBootstrap(t *testing.T) {
	path := writeTempConfig(t, `{
  "custom": {"keep": true},
  "env": {"PORT": "1984"},
  "integrations": {"demo": {"token": "keep"}}
}`)
	def := integrationinterface.Definition{Name: CoreSettingsName}

	if err := MergeValues(path, def, map[string]*string{
		"BOOTSTRAP_GIT": strPtr(`{"owner":"admin"}`),
		"PORT":          strPtr("1985"),
	}); err != nil {
		t.Fatalf("MergeValues: %v", err)
	}

	root := readJSONMap(t, path)
	if _, ok := root["custom"]; !ok {
		t.Fatal("expected unknown top-level key preserved")
	}
	env := root["env"].(map[string]interface{})
	if env["PORT"] != "1985" {
		t.Fatalf("expected env PORT updated, got %#v", env["PORT"])
	}
	if _, exists := env["BOOTSTRAP_GIT"]; exists {
		t.Fatal("did not expect BOOTSTRAP_GIT in env section")
	}
	bootstrap, ok := root["bootstrap"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected bootstrap section, got %#v", root["bootstrap"])
	}
	git, ok := bootstrap["git"].(map[string]interface{})
	if !ok || git["owner"] != "admin" {
		t.Fatalf("expected bootstrap.git.owner admin, got %#v", bootstrap["git"])
	}
	integrations := root["integrations"].(map[string]interface{})
	if _, exists := integrations[CoreSettingsName]; exists {
		t.Fatal("did not expect an integrations.core section")
	}
	if _, exists := integrations["demo"]; !exists {
		t.Fatal("expected existing integrations preserved")
	}

	if err := MergeValues(path, def, map[string]*string{"BOOTSTRAP_GIT": nil}); err != nil {
		t.Fatalf("MergeValues unset: %v", err)
	}
	root = readJSONMap(t, path)
	if _, exists := root["bootstrap"]; exists {
		t.Fatalf("expected bootstrap section removed, got %#v", root["bootstrap"])
	}
}

func TestValidateValuesCoreBootstrap(t *testing.T) {
	def := CoreDefinition()
	if _, err := ValidateValues(def, map[string]*string{"BOOTSTRAP_GIT": strPtr(`{"owner":"admin"}`)}); err != nil {
		t.Fatalf("expected valid JSON to pass: %v", err)
	}
	_, err := ValidateValues(def, map[string]*string{"BOOTSTRAP_GIT": strPtr("not-json")})
	if err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}
