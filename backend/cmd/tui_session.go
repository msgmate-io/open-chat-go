package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tuiSessionDirName is the subdirectory of the device-scoped TUI config
// directory that holds one directory per concurrent session.
const tuiSessionDirName = "sessions"

// SessionTUIConfigDir resolves the private config directory for one concurrent
// TUI session, so several `open-chat code` shells on the same device never
// share (and never clobber) one `config.json`.
//
// The TUI stores servers, tokens and the active selection in a single
// device-scoped file (`~/.config/open-chat-go/tui/config.json` on Linux,
// relocated by `OPEN_CHAT_TUI_CONFIG_DIR`). A second `open-chat code` in
// another shell therefore writes the same file as the first session; atomic
// replace and lock attempts can fail, and even when they succeed one session
// silently overwrites the other's active server/chat selection. Coding chats
// are legitimately run side by side, so the coder integration gives each
// session its own config directory instead of serializing sessions behind a
// device lock.
//
// Resolution order for the base directory: an explicit baseDir argument (for
// embedders and tests), then OPEN_CHAT_TUI_CONFIG_DIR, then the platform user
// config directory. The session directory is
// `<base>/sessions/<sanitized session id>` and is created with 0700 permissions
// because it holds bearer tokens.
func SessionTUIConfigDir(baseDir, sessionID string) (string, error) {
	base, err := tuiConfigBaseDir(baseDir)
	if err != nil {
		return "", err
	}
	session := sanitizeTUISessionID(sessionID)
	if session == "" {
		return "", fmt.Errorf("a session id is required to isolate the TUI config directory")
	}

	dir := filepath.Join(base, tuiSessionDirName, session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create the TUI config directory %s: %w", dir, err)
	}
	return dir, nil
}

// tuiConfigBaseDir picks the device-scoped TUI config base directory.
func tuiConfigBaseDir(baseDir string) (string, error) {
	if base := strings.TrimSpace(baseDir); base != "" {
		return expandUserHome(base), nil
	}
	if base := strings.TrimSpace(os.Getenv("OPEN_CHAT_TUI_CONFIG_DIR")); base != "" {
		return expandUserHome(base), nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve the user config directory for the TUI: %w", err)
	}
	return filepath.Join(configDir, "open-chat-go", "tui"), nil
}

// expandUserHome resolves a leading `~` so a documented `~/...` value works
// without a shell expanding it first.
func expandUserHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

// sanitizeTUISessionID turns an arbitrary session id (chat uuid, endpoint name,
// folder) into a single path element: it must not escape the sessions directory
// or collide across case-insensitive filesystems. Anything outside [a-z0-9]
// becomes a single dash, so `.` is folded as well - a session id must never
// contribute a `..` element, and dots carry no meaning in this directory name.
func sanitizeTUISessionID(sessionID string) string {
	trimmed := strings.ToLower(strings.TrimSpace(sessionID))
	var b strings.Builder
	lastDash := false
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if cleaned == "" {
		return ""
	}
	if len(cleaned) > 64 {
		cleaned = strings.Trim(cleaned[:64], "-")
	}
	return cleaned
}
