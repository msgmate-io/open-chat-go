//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultInstallDir is where the managed binary is copied on Unix hosts.
func defaultInstallDir() string {
	return "/usr/local/bin"
}

// installedBinaryName is the fixed binary name used for the service, even when
// the released artifact carries a version suffix.
func installedBinaryName() string {
	return "open-chat"
}

// defaultWorkDir holds the service database and generated service config.
func defaultWorkDir() string {
	return "/var/lib/open-chat"
}

// isElevated reports whether the process can manage a system-wide service.
func isElevated() bool {
	return os.Geteuid() == 0
}

// ensurePath verifies that installDir is reachable through PATH. Unix install
// locations such as /usr/local/bin are conventionally already on PATH, so this
// only warns instead of mutating the environment.
func ensurePath(installDir string) error {
	pathEnv := os.Getenv("PATH")
	for _, entry := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(entry) == strings.TrimSpace(installDir) {
			return nil
		}
	}
	fmt.Printf("Warning: %s is not on your PATH; add it to run `%s` directly\n", installDir, installedBinaryName())
	return nil
}

// removePath is a no-op on Unix; install locations such as /usr/local/bin are
// managed by the distribution rather than by open-chat.
func removePath(installDir string) error {
	return nil
}
