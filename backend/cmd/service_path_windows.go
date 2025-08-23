//go:build windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// defaultInstallDir is where the managed binary is copied on Windows hosts.
func defaultInstallDir() string {
	base := strings.TrimSpace(os.Getenv("ProgramFiles"))
	if base == "" {
		base = `C:\Program Files`
	}
	return filepath.Join(base, "OpenChat")
}

// installedBinaryName is the fixed binary name used for the service.
func installedBinaryName() string {
	return "open-chat.exe"
}

// defaultWorkDir holds the service database and generated service config.
func defaultWorkDir() string {
	base := strings.TrimSpace(os.Getenv("ProgramData"))
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "OpenChat")
}

// isElevated reports whether the process runs with an elevated (admin) token.
func isElevated() bool {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// ensurePath adds installDir to the persistent user or machine PATH, removing
// duplicates and broadcasting WM_SETTINGCHANGE best-effort so new shells pick
// up the change.
func ensurePath(installDir string) error {
	root, subkey := environmentRegistryTarget()

	k, _, err := registry.CreateKey(root, subkey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open environment registry key: %w", err)
	}
	defer k.Close()

	current, _, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("failed to read PATH: %w", err)
	}

	for _, entry := range filepath.SplitList(current) {
		if strings.EqualFold(strings.TrimSpace(entry), strings.TrimSpace(installDir)) {
			return nil
		}
	}

	updated := strings.TrimRight(strings.TrimSpace(current), ";")
	if updated == "" {
		updated = installDir
	} else {
		updated = updated + ";" + installDir
	}
	if err := k.SetStringValue("Path", updated); err != nil {
		return fmt.Errorf("failed to update PATH: %w", err)
	}
	broadcastEnvironmentChange()
	return nil
}

// removePath drops installDir from the persistent user or machine PATH.
func removePath(installDir string) error {
	root, subkey := environmentRegistryTarget()

	k, err := registry.OpenKey(root, subkey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("failed to open environment registry key: %w", err)
	}
	defer k.Close()

	current, _, err := k.GetStringValue("Path")
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("failed to read PATH: %w", err)
	}

	kept := make([]string, 0)
	removed := false
	for _, entry := range filepath.SplitList(current) {
		if strings.EqualFold(strings.TrimSpace(entry), strings.TrimSpace(installDir)) {
			removed = true
			continue
		}
		if strings.TrimSpace(entry) == "" {
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return nil
	}
	if err := k.SetStringValue("Path", strings.Join(kept, ";")); err != nil {
		return fmt.Errorf("failed to update PATH: %w", err)
	}
	broadcastEnvironmentChange()
	return nil
}

func environmentRegistryTarget() (registry.Key, string) {
	if isElevated() {
		return registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	}
	return registry.CURRENT_USER, `Environment`
}

const (
	hwndBroadcast   = 0xffff
	wmSettingChange = 0x001a
	smtoAbortIfHung = 0x0002
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procSendMessageW = user32.NewProc("SendMessageTimeoutW")
)

// broadcastEnvironmentChange asks running applications to re-read the
// environment. Failures are ignored: the registry update is what persists.
func broadcastEnvironmentChange() {
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = procSendMessageW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(env)),
		uintptr(smtoAbortIfHung),
		uintptr(1000),
		uintptr(unsafe.Pointer(&result)),
	)
}
