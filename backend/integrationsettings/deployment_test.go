package integrationsettings

import (
	"os"
	"path/filepath"
	"testing"

	"backend/runtimecfg"
)

func TestDetectConfigFormat(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "a.json")
	if err := os.WriteFile(jsonPath, []byte(`{"env":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(dir, "a.yaml")
	if err := os.WriteFile(yamlPath, []byte("env:\n  A: b\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		source string
		want   string
	}{
		{"", "none"},
		{"inline --config JSON", "inline"},
		{jsonPath, "json"},
		{yamlPath, "yaml"},
		{filepath.Join(dir, "missing.json"), "unknown"},
	}
	for _, tc := range cases {
		if got := DetectConfigFormat(tc.source); got != tc.want {
			t.Fatalf("DetectConfigFormat(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestBuildDeploymentInfoExplicitType(t *testing.T) {
	t.Setenv("OPEN_CHAT_DEPLOYMENT_TYPE", "managed")
	prev := runtimecfg.GetConfigSource()
	t.Cleanup(func() { runtimecfg.SetConfigSource(prev) })

	dir := t.TempDir()
	path := filepath.Join(dir, "open-chat.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimecfg.SetConfigSource(path)

	info := BuildDeploymentInfo()
	if info.Type != "managed" {
		t.Fatalf("expected managed deployment type, got %q", info.Type)
	}
	if info.ConfigFormat != "json" {
		t.Fatalf("expected json config format, got %q", info.ConfigFormat)
	}
	if !info.CanPersist {
		t.Fatalf("expected writable JSON config to be persistable, reasons: %v", info.Reasons)
	}
}

func TestBuildDeploymentInfoYAMLPersistable(t *testing.T) {
	prev := runtimecfg.GetConfigSource()
	t.Cleanup(func() { runtimecfg.SetConfigSource(prev) })

	dir := t.TempDir()
	path := filepath.Join(dir, "open-chat.yaml")
	if err := os.WriteFile(path, []byte("env:\n  A: b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimecfg.SetConfigSource(path)

	info := BuildDeploymentInfo()
	if info.ConfigFormat != "yaml" {
		t.Fatalf("expected yaml config format, got %q", info.ConfigFormat)
	}
	if !info.CanPersist {
		t.Fatalf("expected writable YAML config to be persistable, reasons: %v", info.Reasons)
	}
}

func TestBuildDeploymentInfoInlineNotPersistable(t *testing.T) {
	prev := runtimecfg.GetConfigSource()
	t.Cleanup(func() { runtimecfg.SetConfigSource(prev) })
	runtimecfg.SetConfigSource("inline --config YAML")

	info := BuildDeploymentInfo()
	if info.CanPersist {
		t.Fatal("expected inline config to not be persistable")
	}
	if len(info.Reasons) == 0 {
		t.Fatal("expected a reason explaining why persistence is unavailable")
	}
}
