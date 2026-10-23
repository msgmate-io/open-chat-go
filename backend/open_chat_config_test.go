package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadOpenChatConfigAcceptsBootstrapUsers proves the open-chat config JSON
// schema (decoded with DisallowUnknownFields) accepts a bootstrap.users section
// and surfaces it as a user spec for the runtime bootstrap.
func TestLoadOpenChatConfigAcceptsBootstrapUsers(t *testing.T) {
	raw := []byte(`{
		"env": {"LITELLM_API_HOST": "https://litellm.example/v1"},
		"bootstrap": {
			"users": [
				{"username": "bootstrap_admin", "password": "StrongPass1!", "is_admin": true},
				{"username": "bootstrap_bot", "password": "StrongPass1!", "is_automated": true}
			]
		}
	}`)

	cfg, err := loadOpenChatConfig(raw, "inline test config")
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected bootstrap.users config: %v", err)
	}
	if cfg.Bootstrap == nil {
		t.Fatalf("expected non-nil bootstrap")
	}

	out := toOpenChatBootstrapRuntime(cfg)
	if len(out.UserSpecs) != 1 {
		t.Fatalf("expected 1 user spec, got %d", len(out.UserSpecs))
	}
}

// TestLoadOpenChatConfigAcceptsBootstrapGit proves the strict open-chat config
// schema (decoded with DisallowUnknownFields) accepts the previously-rejected
// `bootstrap.git` section and surfaces it as git bootstrap specs.
func TestLoadOpenChatConfigAcceptsBootstrapGit(t *testing.T) {
	raw := []byte(`
bootstrap:
  git:
    owner: [admin]
    tokens:
      - name: github-main
        provider: github
        token: "$anchors.github-main-token"
        account_username: cur1ousdude
    repositories:
      - name: my-app
        remote_url: https://github.com/org/my-app.git
        auth_mode: token
        token_name: github-main
    workspaces:
      - name: my-app-dev
        repository_name: my-app
        ssh_server_name: devhost
        project_path: /srv/git/my-app
        git_user_name: cur1ousdude
        git_user_email: "123456+cur1ousdude@users.noreply.github.com"
    triggers:
      - repository_name: my-app
        events: [assign, mention]
        plan_bot_uuid: issue-plan-bot
        coding_bot_uuid: coding-agent
        prompt_template: "Handle {{url}}"
        post_badge_comment: false
anchors:
  github-main-token: ghp_x
`)

	cfg, err := loadOpenChatConfig(raw, "bootstrap.git yaml")
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected bootstrap.git: %v", err)
	}
	if cfg.Bootstrap == nil || cfg.Bootstrap.Git == nil {
		t.Fatalf("expected bootstrap.git to be parsed")
	}
	out := toOpenChatBootstrapRuntime(cfg)
	if len(out.GitTokenSpecs) != 1 || len(out.GitRepositorySpecs) != 1 || len(out.GitWorkspaceSpecs) != 1 {
		t.Fatalf("bootstrap.git not mapped to runtime specs: %+v", out)
	}
	if !strings.Contains(out.GitTokenSpecs[0], "account_username") {
		t.Fatalf("token account identity missing from git spec: %s", out.GitTokenSpecs[0])
	}
	if !strings.Contains(out.GitWorkspaceSpecs[0], "git_user_name") {
		t.Fatalf("workspace identity missing from git spec: %s", out.GitWorkspaceSpecs[0])
	}
	if len(out.GitDefaultOwners) != 1 || out.GitDefaultOwners[0] != "admin" {
		t.Fatalf("unexpected GitDefaultOwners: %+v", out.GitDefaultOwners)
	}
	if len(out.GitTriggerSpecs) != 1 {
		t.Fatalf("bootstrap.git.triggers not mapped to runtime specs: %+v", out)
	}
	if !strings.Contains(out.GitTriggerSpecs[0], "issue-plan-bot") {
		t.Fatalf("trigger bot missing from git trigger spec: %s", out.GitTriggerSpecs[0])
	}
	if !strings.Contains(out.GitTriggerSpecs[0], "post_badge_comment") {
		t.Fatalf("trigger badge-comment setting missing from git trigger spec: %s", out.GitTriggerSpecs[0])
	}
}

// TestLoadOpenChatConfigAcceptsBootstrapMCP proves the strict open-chat config
// schema (decoded with DisallowUnknownFields) accepts the `bootstrap.mcp`
// section and surfaces it as MCP bootstrap specs, so deployments can
// pre-register Google Workspace MCP servers with OAuth client credentials.
func TestLoadOpenChatConfigAcceptsBootstrapMCP(t *testing.T) {
	raw := []byte(`
bootstrap:
  mcp:
    owners: [admin]
    servers:
      - name: google-drive
        template: google_workspace_drive
        config:
          auth:
            client_id: "$anchors.google_client_id"
            client_secret: "$anchors.google_client_secret"
            redirect_uri: "https://chat.example.com/callback"
      - name: google-sheets
        template: google_workspace_sheets
        config:
          auth:
            client_id: "$anchors.google_client_id"
            client_secret: "$anchors.google_client_secret"
anchors:
  google_client_id: "123-abc.apps.googleusercontent.com"
  google_client_secret: "GOCSPX-secret"
`)

	cfg, err := loadOpenChatConfig(raw, "bootstrap.mcp yaml")
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected bootstrap.mcp: %v", err)
	}
	if cfg.Bootstrap == nil || cfg.Bootstrap.MCP == nil {
		t.Fatalf("expected bootstrap.mcp to be parsed")
	}
	out := toOpenChatBootstrapRuntime(cfg)
	if len(out.MCPServerSpecs) != 1 {
		t.Fatalf("expected 1 mcp server spec, got %d", len(out.MCPServerSpecs))
	}
	if !strings.Contains(out.MCPServerSpecs[0], "google_workspace_drive") ||
		!strings.Contains(out.MCPServerSpecs[0], "google_workspace_sheets") {
		t.Fatalf("mcp server spec missing entries: %s", out.MCPServerSpecs[0])
	}
	if !strings.Contains(out.MCPServerSpecs[0], "client_secret") {
		t.Fatalf("mcp server spec missing auth credentials: %s", out.MCPServerSpecs[0])
	}
	if len(out.MCPDefaultOwners) != 1 || out.MCPDefaultOwners[0] != "admin" {
		t.Fatalf("unexpected MCPDefaultOwners: %+v", out.MCPDefaultOwners)
	}
}

// TestLoadOpenChatConfigAcceptsYAMLWithAnchors proves the loader accepts YAML
// config documents (including anchors/aliases and multi-line strings) and
// applies the same strict schema as JSON configs.
func TestLoadOpenChatConfigAcceptsYAMLWithAnchors(t *testing.T) {
	raw := []byte(`
anchors:
  shared_token: &shared-token "vault-token-8f2"
  kubeconfig: &kubeconfig: |
    apiVersion: v1
    kind: Config
integrations:
  sample_runtime:
    kubeconfig: *kubeconfig
  sample_git:
    bootstrap_tokens:
      - name: ci-token
        provider: github
        token: *shared-token
env:
  NODE_VERSION: "16"
bootstrap:
  ssh:
    owner: admin
    keys:
      - name: sandbox-key
        comment: sandbox
        private_key: *shared-token
`)
	raw = []byte(`
anchors:
  shared_token: &shared-token "vault-token-8f2"
  kubeconfig: &kubeconfig |
    apiVersion: v1
    clusters: []
integrations:
  sample_runtime:
    kubeconfig: *kubeconfig
  sample_git:
    bootstrap_tokens:
      - name: ci-token
        provider: github
        token: *shared-token
env:
  NODE_VERSION: "16"
`)

	cfg, err := loadOpenChatConfig(raw, "yaml config")
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected YAML config: %v", err)
	}
	if got := cfg.Integrations["sample_git"]["bootstrap_tokens"]; got == nil {
		t.Fatalf("expected integration bootstrap_tokens from YAML")
	}
	if _, ok := cfg.Integrations["sample_runtime"]["kubeconfig"]; !ok {
		t.Fatalf("expected sample_runtime kubeconfig alias to resolve")
	}
	tokens, ok := cfg.Integrations["sample_git"]["bootstrap_tokens"].([]interface{})
	if !ok || len(tokens) != 1 {
		t.Fatalf("expected one git bootstrap token from YAML alias, got %#v", cfg.Integrations["sample_git"]["bootstrap_tokens"])
	}
	tokenRow, _ := tokens[0].(map[string]interface{})
	if tokenRow["token"] != "vault-token-8f2" {
		t.Fatalf("bootstrap token alias did not resolve to the anchored value: %#v", tokenRow)
	}
	// anchors key must not crash the strict schema
	if len(cfg.Anchors) == 0 {
		t.Fatalf("expected anchors section to be carried through")
	}
}

// TestLoadOpenChatConfigYamlTrailingAnchors covers the recommended authoring
// style: all long artifacts (kubeconfigs, SSH keys, ...) live in a trailing
// `anchors` block and are referenced inline with "$anchors.<name>" markers the
// loader resolves after parsing (standard YAML aliases cannot be
// forward-referenced).
func TestLoadOpenChatConfigYamlTrailingAnchors(t *testing.T) {
	raw := []byte(`
integrations:
  sample_runtime:
    kubeconfig: "$anchors.kubeconfig"
  sample_git:
    bootstrap_tokens:
      - name: ci-token
        provider: github
        token: "$anchors.ci_token"
  sample_nested:
    kubeconfig_yaml: "$anchors.nested"
env:
  NODE_VERSION: "16"
bootstrap:
  ssh:
    owner: admin
    keys:
      - name: sandbox-key
        private_key: "$anchors.ci_token"

# Long artifacts referenced above live here at the very end.
anchors:
  ci_token: vault-token-8f2
  kubeconfig: |
    apiVersion: v1
    kind: Config
    clusters: []
  nested: "$anchors.kubeconfig"
`)

	cfg, err := loadOpenChatConfig(raw, "yaml config")
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected trailing-anchor YAML config: %v", err)
	}
	if kube, _ := cfg.Integrations["sample_runtime"]["kubeconfig"].(string); kube != "apiVersion: v1\nkind: Config\nclusters: []\n" {
		t.Fatalf("kubeconfig $anchors reference did not resolve: %q", kube)
	}
	if token, _ := cfg.Integrations["sample_git"]["bootstrap_tokens"].([]interface{})[0].(map[string]interface{})["token"].(string); token != "vault-token-8f2" {
		t.Fatalf("token $anchors reference did not resolve: %q", token)
	}
	if key := cfg.Bootstrap.SSH; key == nil {
		t.Fatalf("expected ssh bootstrap from trailing-anchor YAML config")
	}
	if nested := cfg.Integrations["sample_nested"]["kubeconfig_yaml"]; nested != "apiVersion: v1\nkind: Config\nclusters: []\n" {
		t.Fatalf("nested $anchors reference did not resolve: %#v", nested)
	}
	for _, values := range []map[string]interface{}{cfg.Env, cfg.Integrations["sample_runtime"]} {
		for _, value := range values {
			if str, ok := value.(string); ok && strings.HasPrefix(str, "$anchors.") {
				t.Fatalf("unresolved $anchors reference found: %q", str)
			}
		}
	}
}

// TestLoadOpenChatConfigYamlEmbeddedAnchorRef verifies anchors can be embedded
// inside larger strings (e.g. multi-line setup commands), not only as whole
// values.
func TestLoadOpenChatConfigYamlEmbeddedAnchorRef(t *testing.T) {
	raw := []byte(`
env:
  SETUP_SCRIPT: |
    echo start
    printf '%s' "$anchors.blob" | base64 -d > /tmp/blob
    echo "$anchors.marker done"
anchors:
  blob: "BASE64DATA"
  marker: "MARK"
`)

	cfg, err := loadOpenChatConfig(raw, "embedded anchor yaml")
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected embedded-anchor YAML: %v", err)
	}
	script, _ := cfg.Env["SETUP_SCRIPT"].(string)
	if !strings.Contains(script, "BASE64DATA") || !strings.Contains(script, "MARK done") {
		t.Fatalf("embedded anchors not resolved: %q", script)
	}
	if strings.Contains(script, "$anchors.") {
		t.Fatalf("unresolved embedded anchor reference left in %q", script)
	}
}

// TestLoadOpenChatConfigYamlUnknownAnchorRef makes sure references to missing
// anchors fail with a helpful message instead of silently passing through.
func TestLoadOpenChatConfigYamlUnknownAnchorRef(t *testing.T) {
	raw := []byte("env:\n  NODE_VERSION: \"$anchors.does_not_exist\"\n")
	_, err := loadOpenChatConfig(raw, "yaml config")
	if err == nil || !strings.Contains(err.Error(), "$anchors.") {
		t.Fatalf("expected unknown-anchor reference error, got %v", err)
	}
}

// TestLoadOpenChatConfigYamlUnknownNamedAnchorRef requires the error to name
// the missing anchor so misconfigured reference names are easy to spot.
func TestLoadOpenChatConfigYamlUnknownNamedAnchorRef(t *testing.T) {
	raw := []byte("anchors:\n  other: x\nenv:\n  NODE_VERSION: \"$anchors.does_not_exist\"\n")
	_, err := loadOpenChatConfig(raw, "yaml config")
	if err == nil || !strings.Contains(err.Error(), "does_not_exist") {
		t.Fatalf("expected unknown-anchor reference error, got %v", err)
	}
}

// TestLoadOpenChatConfigJSONResolvesTrailingAnchors guards against the JSON
// fast-path bypassing "$anchors." reference resolution. Minified JSON (as
// emitted by the CI config-delta publisher) is valid YAML too, so a JSON
// config must resolve its trailing anchors exactly like the YAML variant;
// otherwise long artifacts (SSH private keys, kubeconfigs) reach the runtime
// as the literal "$anchors.<name>" string and break bootstrap.
func TestLoadOpenChatConfigJSONResolvesTrailingAnchors(t *testing.T) {
	raw := []byte(`{"env":{"NODE_VERSION":"$anchors.node_version"},"bootstrap":{"ssh":{"owner":"admin","keys":[{"name":"sandbox-key","private_key":"$anchors.ssh_key"}]}},"anchors":{"node_version":"16","ssh_key":"vault-key-data"}}`)

	cfg, err := loadOpenChatConfig(raw, "minified json config")
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected minified JSON config: %v", err)
	}
	if cfg.Env["NODE_VERSION"] != "16" {
		t.Fatalf("JSON $anchors reference did not resolve: %#v", cfg.Env["NODE_VERSION"])
	}
	if cfg.Bootstrap == nil || cfg.Bootstrap.SSH == nil {
		t.Fatalf("expected bootstrap.ssh to be parsed from JSON config")
	}
	var keys []struct {
		Name       string `json:"name"`
		PrivateKey string `json:"private_key"`
	}
	if err := json.Unmarshal(cfg.Bootstrap.SSH.Keys, &keys); err != nil {
		t.Fatalf("failed decoding ssh keys: %v", err)
	}
	if len(keys) != 1 || keys[0].PrivateKey != "vault-key-data" {
		t.Fatalf("JSON $anchors reference did not resolve in ssh key: %#v", keys)
	}
}

// TestLoadOpenChatConfigJSONUnknownAnchorRef makes sure an unknown reference in
// a JSON config fails with a helpful message instead of passing the literal
// marker through to the runtime.
func TestLoadOpenChatConfigJSONUnknownAnchorRef(t *testing.T) {
	raw := []byte(`{"env":{"NODE_VERSION":"$anchors.does_not_exist"},"anchors":{"other":"x"}}`)
	_, err := loadOpenChatConfig(raw, "minified json config")
	if err == nil || !strings.Contains(err.Error(), "does_not_exist") {
		t.Fatalf("expected unknown-anchor reference error from JSON config, got %v", err)
	}
}

// TestResolveConfigSourceInlineYAML guards against passing the whole YAML
// config document inline (e.g. via the OPEN_CHAT_CONFIG env var): content with
// line breaks cannot be a file path and must be parsed as an inline document,
// and extremely long single-line specs must fall back to inline parsing
// instead of surfacing "file name too long".
func TestResolveConfigSourceInlineYAML(t *testing.T) {
	multiLine := "env:\n  NODE_VERSION: \"16\"\n"
	content, source, err := resolveConfigSource(multiLine)
	if err != nil {
		t.Fatalf("inline multi-line YAML spec failed: %v", err)
	}
	if source != "inline --config YAML" {
		t.Fatalf("unexpected source label %q", source)
	}
	cfg, err := loadOpenChatConfig(content, source)
	if err != nil {
		t.Fatalf("inline multi-line YAML config failed to load: %v", err)
	}
	if cfg.Env["NODE_VERSION"] != "16" {
		t.Fatalf("env not parsed from inline YAML: %#v", cfg.Env)
	}

	long := "env: { SMOKE_KEY: " + strings.Repeat("A", 5000) + " }"
	content, _, err = resolveConfigSource(long)
	if err != nil {
		t.Fatalf("long single-line inline YAML spec failed: %v", err)
	}
	if _, err := loadOpenChatConfig(content, "inline --config YAML"); err != nil {
		t.Fatalf("long single-line inline YAML failed to load: %v", err)
	}
}

// TestLoadOpenChatConfigResolvesLinkedFiles covers ci#759: a config value may
// link a text file with a "$file.<path>" marker instead of inlining it, which
// lets authors keep markdown prompts and other long artifacts as real files in
// their config directory. Paths resolve relative to the config file.
func TestLoadOpenChatConfigResolvesLinkedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	body := "# System prompt\n\nBe helpful.\n"
	if err := os.WriteFile(filepath.Join(dir, "prompts", "system.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write linked file: %v", err)
	}
	raw := []byte("env:\n  SYSTEM_PROMPT: \"$file.prompts/system.md\"\n  OTHER: inline\n")

	cfg, err := loadOpenChatConfig(raw, filepath.Join(dir, "open-chat.yaml"))
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected a $file. link: %v", err)
	}
	if got := cfg.Env["SYSTEM_PROMPT"]; got != body {
		t.Fatalf("linked file content not substituted: %#v", got)
	}
	if got := cfg.Env["OTHER"]; got != "inline" {
		t.Fatalf("unrelated value changed: %#v", got)
	}
}

// TestLoadOpenChatConfigLinkedFileJSON guards the JSON fast-path: minified JSON
// (what the CI config publisher emits) must resolve "$file." links exactly like
// the YAML variant instead of handing the literal marker to the runtime.
func TestLoadOpenChatConfigLinkedFileJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("linked notes"), 0o644); err != nil {
		t.Fatalf("write linked file: %v", err)
	}
	raw := []byte("{\"env\":{\"NOTES\":\"$file.notes.md\"}}")

	cfg, err := loadOpenChatConfig(raw, filepath.Join(dir, "open-chat.json"))
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected a JSON $file. link: %v", err)
	}
	if got := cfg.Env["NOTES"]; got != "linked notes" {
		t.Fatalf("JSON $file. link did not resolve: %#v", got)
	}
}

// TestLoadOpenChatConfigLinkedFileErrors makes sure a broken link fails the boot
// with a message naming the problem instead of silently passing the marker (or
// a binary blob) through to the runtime.
func TestLoadOpenChatConfigLinkedFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "binary.md"), []byte("text\x00binary"), 0o644); err != nil {
		t.Fatalf("write binary file: %v", err)
	}

	cases := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"missing file", "env:\n  PROMPT: \"$file.nope.md\"\n", "nope.md"},
		{"directory", "env:\n  PROMPT: \"$file.prompts\"\n", "is a directory"},
		{"binary file", "env:\n  PROMPT: \"$file.binary.md\"\n", "not a text file"},
		{"escapes config dir", "env:\n  PROMPT: \"$file.../outside.md\"\n", "outside the config directory"},
		{"absolute path", "env:\n  PROMPT: \"$file./etc/hostname\"\n", "relative to the config directory"},
		{"embedded marker", "env:\n  SCRIPT: |\n    echo \"$file.notes.md\"\n", "entire config value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadOpenChatConfig([]byte(tc.raw), filepath.Join(dir, "open-chat.yaml"))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected an error mentioning %q, got %v", tc.wantErr, err)
			}
		})
	}
}
