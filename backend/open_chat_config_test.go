package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

// TestStagingOpenChatConfigLoads guards that the committed staging config stays
// parseable by the real loader. It skips when the file is not present (e.g. the
// dev container does not mount development/).
func TestStagingOpenChatConfigLoads(t *testing.T) {
	path := filepath.Join("..", "development", "ci", "open-chat-staging.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("staging config not present at %s: %v", path, err)
	}

	if raw[0] != '{' {
		t.Fatalf("config must start with '{' to be usable as inline OPEN_CHAT_CONFIG, got %q", raw[0])
	}

	cfg, err := loadOpenChatConfig(raw, path)
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected staging config: %v", err)
	}
	if len(cfg.Env) == 0 {
		t.Fatalf("expected non-empty env section")
	}
}

// TestDockerSandboxConfigLoadsAndConnects verifies that open-chat.json correctly
// parses and configures the docker-sandbox SSH key, server, and OpenCode project,
// and tests live SSH connectivity to the dev-sandbox container if running.
func TestDockerSandboxConfigLoadsAndConnects(t *testing.T) {
	candidates := []string{
		"open-chat.json",
		filepath.Join("..", "open-chat.json"),
		filepath.Join("..", "development", "ci", "open-chat-sandbox-benchmark.json"),
		filepath.Join("development", "ci", "open-chat-sandbox-benchmark.json"),
	}
	var raw []byte
	var loadedPath string
	for _, c := range candidates {
		if data, err := os.ReadFile(c); err == nil {
			raw = data
			loadedPath = c
			break
		}
	}
	if len(raw) == 0 {
		t.Skipf("no sandbox config found in %v (skipping)", candidates)
	}

	cfg, err := loadOpenChatConfig(raw, loadedPath)
	if err != nil {
		t.Fatalf("loadOpenChatConfig failed: %v", err)
	}
	if cfg.Bootstrap == nil || cfg.Bootstrap.SSH == nil {
		t.Fatalf("missing bootstrap.ssh in open-chat.json")
	}

	// Verify SSH key
	type keySpec struct {
		Name       string `json:"name"`
		PrivateKey string `json:"private_key"`
	}
	var keys []keySpec
	if err := json.Unmarshal(cfg.Bootstrap.SSH.Keys, &keys); err != nil {
		t.Fatalf("failed decoding ssh keys: %v", err)
	}
	var sandboxKey *keySpec
	for _, k := range keys {
		if k.Name == "docker-sandbox" {
			kCopy := k
			sandboxKey = &kCopy
			break
		}
	}
	if sandboxKey == nil {
		t.Fatalf("docker-sandbox key not found in bootstrap.ssh.keys")
	}

	// Verify SSH server
	type serverSpec struct {
		Name       string `json:"name"`
		SSHKeyName string `json:"ssh_key_name"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		Username   string `json:"username"`
	}
	var servers []serverSpec
	if err := json.Unmarshal(cfg.Bootstrap.SSH.Servers, &servers); err != nil {
		t.Fatalf("failed decoding ssh servers: %v", err)
	}
	var sandboxServer *serverSpec
	for _, s := range servers {
		if s.Name == "docker-sandbox" {
			sCopy := s
			sandboxServer = &sCopy
			break
		}
	}
	if sandboxServer == nil {
		t.Fatalf("docker-sandbox server not found in bootstrap.ssh.servers")
	}

	// Verify OpenCode project
	if cfg.Bootstrap.Opencode == nil {
		t.Fatalf("missing bootstrap.opencode in open-chat.json")
	}
	type projectSpec struct {
		Name          string `json:"name"`
		Mode          string `json:"mode"`
		SSHServerName string `json:"ssh_server_name"`
		ProjectPath   string `json:"project_path"`
	}
	var projects []projectSpec
	if err := json.Unmarshal(cfg.Bootstrap.Opencode.Projects, &projects); err != nil {
		t.Fatalf("failed decoding opencode projects: %v", err)
	}
	var sandboxProject *projectSpec
	for _, p := range projects {
		if p.Name == "docker-sandbox" {
			pCopy := p
			sandboxProject = &pCopy
			break
		}
	}
	if sandboxProject == nil {
		t.Fatalf("docker-sandbox project not found in bootstrap.opencode.projects")
	}
	if sandboxProject.Mode != "managed_ssh" || sandboxProject.SSHServerName != "docker-sandbox" {
		t.Fatalf("unexpected sandbox project config: %+v", sandboxProject)
	}

	// Live SSH test if host is resolvable / reachable
	signer, err := ssh.ParsePrivateKey([]byte(sandboxKey.PrivateKey))
	if err != nil {
		t.Fatalf("failed parsing private key: %v", err)
	}

	addr := net.JoinHostPort(sandboxServer.Host, strconv.Itoa(sandboxServer.Port))
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            sandboxServer.Username,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Logf("dev-sandbox is not reachable at %s (skipping live SSH test): %v", addr, err)
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("failed opening ssh session: %v", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput("echo live-ssh-test-ok && opencode --version")
	if err != nil {
		t.Fatalf("failed executing remote command: %v, output: %s", err, string(output))
	}
	if !strings.Contains(string(output), "live-ssh-test-ok") {
		t.Fatalf("unexpected ssh command output: %s", string(output))
	}
	t.Logf("Live SSH to %s succeeded! Output: %s", addr, strings.TrimSpace(string(output)))
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
  docker_sandbox:
    kubeconfig: *kubeconfig
  git:
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
  docker_sandbox:
    kubeconfig: *kubeconfig
  git:
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
	if got := cfg.Integrations["git"]["bootstrap_tokens"]; got == nil {
		t.Fatalf("expected git bootstrap_tokens from YAML")
	}
	if _, ok := cfg.Integrations["docker_sandbox"]["kubeconfig"]; !ok {
		t.Fatalf("expected docker_sandbox kubeconfig alias to resolve")
	}
	tokens, ok := cfg.Integrations["git"]["bootstrap_tokens"].([]interface{})
	if !ok || len(tokens) != 1 {
		t.Fatalf("expected one git bootstrap token from YAML alias, got %#v", cfg.Integrations["git"]["bootstrap_tokens"])
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
  docker_sandbox:
    kubeconfig: "$anchors.kubeconfig"
  git:
    bootstrap_tokens:
      - name: ci-token
        provider: github
        token: "$anchors.ci_token"
  kubernetes:
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
	if kube, _ := cfg.Integrations["docker_sandbox"]["kubeconfig"].(string); kube != "apiVersion: v1\nkind: Config\nclusters: []\n" {
		t.Fatalf("kubeconfig $anchors reference did not resolve: %q", kube)
	}
	if token, _ := cfg.Integrations["git"]["bootstrap_tokens"].([]interface{})[0].(map[string]interface{})["token"].(string); token != "vault-token-8f2" {
		t.Fatalf("token $anchors reference did not resolve: %q", token)
	}
	if key := cfg.Bootstrap.SSH; key == nil {
		t.Fatalf("expected ssh bootstrap from trailing-anchor YAML config")
	}
	if nested := cfg.Integrations["kubernetes"]["kubeconfig_yaml"]; nested != "apiVersion: v1\nkind: Config\nclusters: []\n" {
		t.Fatalf("nested $anchors reference did not resolve: %#v", nested)
	}
	for _, values := range []map[string]interface{}{cfg.Env, cfg.Integrations["docker_sandbox"]} {
		for _, value := range values {
			if str, ok := value.(string); ok && strings.HasPrefix(str, "$anchors.") {
				t.Fatalf("unresolved $anchors reference found: %q", str)
			}
		}
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

// TestCIMsgmateYamlConfigLoads guards the YAML CI config variant (anchors +
// multi-line kubeconfigs / SSH keys) against loader regressions.
func TestCIMsgmateYamlConfigLoads(t *testing.T) {
	path := filepath.Join("..", "development", "ci", "open-chat-ci.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("yaml CI config not present at %s: %v", path, err)
	}
	cfg, err := loadOpenChatConfig(raw, path)
	if err != nil {
		t.Fatalf("loadOpenChatConfig rejected YAML CI config: %v", err)
	}
	if len(cfg.Integrations) == 0 || cfg.Bootstrap == nil {
		t.Fatalf("expected integrations + bootstrap in yaml CI config")
	}
	if _, ok := cfg.Integrations["docker_sandbox"]; !ok {
		t.Fatalf("missing docker_sandbox integration")
	}
	// The kubeconfig must come from the alias, not from a literal "*ref".
	dsb := cfg.Integrations["docker_sandbox"]
	if kube, _ := dsb["kubeconfig"].(string); !strings.HasPrefix(kube, "apiVersion") {
		t.Fatalf("docker_sandbox kubeconfig alias did not resolve: %q", kube[:min(len(kube), 40)])
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
