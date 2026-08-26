package main

import (
	"os"
	"path/filepath"
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
