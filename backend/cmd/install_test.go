package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildServiceArguments(t *testing.T) {
	got := buildServiceArguments("/var/lib/open-chat/open-chat.json", "127.0.0.1", 1984, "/var/lib/open-chat/data.db")
	want := []string{
		"--config", "/var/lib/open-chat/open-chat.json",
		"service", "run",
		"--host", "127.0.0.1",
		"--port", "1984",
		"--db-path", "/var/lib/open-chat/data.db",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildServiceArguments mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildServiceArgumentsWithoutConfig(t *testing.T) {
	got := buildServiceArguments("", "0.0.0.0", 9000, "")
	want := []string{
		"service", "run",
		"--host", "0.0.0.0",
		"--port", "9000",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildServiceArguments mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildServiceEnv(t *testing.T) {
	env := buildServiceEnv("admin:secret", "/tmp/data.db", "127.0.0.1", 1984)
	want := map[string]string{
		"ROOT_CREDENTIALS": "admin:secret",
		"DB_PATH":          "/tmp/data.db",
		"HOST":             "127.0.0.1",
		"PORT":             "1984",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("buildServiceEnv mismatch:\n got: %#v\nwant: %#v", env, want)
	}
}

func TestWriteServiceConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", serviceConfigFileName)
	env := buildServiceEnv("admin:secret", filepath.Join(dir, "data.db"), "127.0.0.1", 1984)

	if err := writeServiceConfigFile(path, env); err != nil {
		t.Fatalf("writeServiceConfigFile returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat config file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file permissions = %o, want 600", perm)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("failed to stat config directory: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config directory permissions = %o, want 700", perm)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	var cfg serviceEnvConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("failed to decode config file: %v", err)
	}
	if !reflect.DeepEqual(cfg.Env, env) {
		t.Fatalf("config env mismatch:\n got: %#v\nwant: %#v", cfg.Env, env)
	}
}

func TestInstallBinaryCopiesExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bin", installedBinaryName())

	if err := installBinary(target); err != nil {
		t.Fatalf("installBinary returned error: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("failed to stat installed binary: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("installed binary is empty")
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable (mode %o)", info.Mode().Perm())
	}

	// Reinstalling over an existing target should also succeed.
	if err := installBinary(target); err != nil {
		t.Fatalf("second installBinary returned error: %v", err)
	}
}
