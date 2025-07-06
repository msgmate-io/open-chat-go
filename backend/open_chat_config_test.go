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
	candidates := []string{"open-chat.json", filepath.Join("..", "open-chat.json")}
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
		t.Fatalf("could not find open-chat.json in %v", candidates)
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
