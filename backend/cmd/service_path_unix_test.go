//go:build !windows

package cmd

import (
	"path/filepath"
	"testing"
)

func TestExtraBinarySearchPathsIncludesDistroLocation(t *testing.T) {
	paths := extraBinarySearchPaths()
	want := "/usr/bin/open-chat"
	for _, p := range paths {
		if p == want {
			return
		}
	}
	t.Fatalf("extraBinarySearchPaths() = %#v, want it to contain %q", paths, want)
}

func TestDefaultInstallDirIsManagedLocation(t *testing.T) {
	if got := defaultInstallDir(); got != "/usr/local/bin" {
		t.Fatalf("defaultInstallDir() = %q, want /usr/local/bin", got)
	}
}

func TestPackageManagedServicePaths(t *testing.T) {
	if filepath.Base(packagedServiceUnitPath) != "open-chat.service" {
		t.Fatalf("packagedServiceUnitPath = %q", packagedServiceUnitPath)
	}
	if filepath.Base(localServiceUnitPath) != "open-chat.service" {
		t.Fatalf("localServiceUnitPath = %q", localServiceUnitPath)
	}
}
