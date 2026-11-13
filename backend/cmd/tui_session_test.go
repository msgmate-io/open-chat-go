package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionTUIConfigDirIsolatesConcurrentSessions(t *testing.T) {
	base := t.TempDir()

	first, err := SessionTUIConfigDir(base, "chat-11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatalf("failed to resolve the first session dir: %v", err)
	}
	second, err := SessionTUIConfigDir(base, "chat-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatalf("failed to resolve the second session dir: %v", err)
	}

	if first == second {
		t.Fatalf("expected two concurrent sessions to get distinct config dirs, both got %s", first)
	}
	// ci#1070: two `open-chat code` shells must never share one config.json.
	if filepath.Dir(first) != filepath.Dir(second) {
		t.Fatalf("expected the session dirs to live under one sessions/ parent, got %s and %s", first, second)
	}
	if filepath.Base(filepath.Dir(first)) != tuiSessionDirName {
		t.Fatalf("expected the session parent to be %q, got %s", tuiSessionDirName, filepath.Dir(first))
	}

	// The directory must exist (the TUI writes config.json into it directly)
	// and must be private because it holds bearer tokens.
	info, err := os.Stat(first)
	if err != nil {
		t.Fatalf("expected the session dir to be created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory", first)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("expected the session dir to be private, got mode %o", perm)
	}
}

func TestSessionTUIConfigDirSanitizesSessionID(t *testing.T) {
	base := t.TempDir()
	cases := map[string]string{
		"Chat/../escape":      "chat-escape",
		"  Mixed Case UUID  ": "mixed-case-uuid",
		"../../etc/passwd":    "etc-passwd",
		"folder with spaces":  "folder-with-spaces",
		"ünïcode-folder":      "n-code-folder",
		"a/b\\c":              "a-b-c",
		"--already-dashed--":  "already-dashed",
	}
	for input, wantSuffix := range cases {
		dir, err := SessionTUIConfigDir(base, input)
		if err != nil {
			t.Fatalf("SessionTUIConfigDir(%q) failed: %v", input, err)
		}
		got := filepath.Base(dir)
		if got != wantSuffix {
			t.Fatalf("SessionTUIConfigDir(%q) = %q, want %q", input, got, wantSuffix)
		}
		if rel, err := filepath.Rel(base, dir); err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("session dir %s escaped the base directory %s", dir, base)
		}
	}
}

func TestSessionTUIConfigDirRequiresSessionID(t *testing.T) {
	if _, err := SessionTUIConfigDir(t.TempDir(), "  ---  "); err == nil {
		t.Fatal("expected an error for a session id that sanitizes to nothing")
	}
}

func TestSessionTUIConfigDirHonoursConfigDirEnv(t *testing.T) {
	envBase := filepath.Join(t.TempDir(), "tui-env")
	t.Setenv("OPEN_CHAT_TUI_CONFIG_DIR", envBase)

	dir, err := SessionTUIConfigDir("", "chat-env")
	if err != nil {
		t.Fatalf("failed to resolve the session dir from the env override: %v", err)
	}
	want := filepath.Join(envBase, tuiSessionDirName, "chat-env")
	if dir != want {
		t.Fatalf("SessionTUIConfigDir() = %s, want %s", dir, want)
	}

	// An explicit base always wins over the environment so embedders can pin it.
	explicit := t.TempDir()
	dir, err = SessionTUIConfigDir(explicit, "chat-env")
	if err != nil {
		t.Fatalf("failed to resolve the session dir from an explicit base: %v", err)
	}
	if !strings.HasPrefix(dir, explicit) {
		t.Fatalf("expected the explicit base to win, got %s", dir)
	}
}

func TestExpandUserHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no user home directory available: %v", err)
	}
	if got := expandUserHome("~"); got != home {
		t.Fatalf("expandUserHome(\"~\") = %s, want %s", got, home)
	}
	if got := expandUserHome("~/x/y"); got != filepath.Join(home, "x", "y") {
		t.Fatalf("expandUserHome(\"~/x/y\") = %s, want %s", got, filepath.Join(home, "x", "y"))
	}
	if got := expandUserHome("/abs/path"); got != "/abs/path" {
		t.Fatalf("expandUserHome(\"/abs/path\") = %s, want the input unchanged", got)
	}
}
