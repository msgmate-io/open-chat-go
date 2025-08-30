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

// packagedServiceUnitPath is where a distribution package installs the unit.
const packagedServiceUnitPath = "/usr/lib/systemd/system/open-chat.service"

// localServiceUnitPath is where `open-chat install` writes the unit.
const localServiceUnitPath = "/etc/systemd/system/open-chat.service"

// extraBinarySearchPaths lists additional locations a distribution-managed
// open-chat binary may live in (e.g. /usr/bin from a .deb package). The managed
// install location is always checked first by callers.
func extraBinarySearchPaths() []string {
	return []string{"/usr/bin/open-chat", "/usr/local/bin/open-chat"}
}

// isPackageManagedService reports whether the service unit comes from a
// distribution package rather than `open-chat install`, so uninstall can defer
// to the package manager instead of failing to remove a missing unit.
func isPackageManagedService() bool {
	if _, err := os.Stat(localServiceUnitPath); err == nil {
		return false
	}
	_, err := os.Stat(packagedServiceUnitPath)
	return err == nil
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
