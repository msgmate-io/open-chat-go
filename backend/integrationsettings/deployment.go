package integrationsettings

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"backend/runtimecfg"
	"backend/servicecontrol"
)

// DeploymentInfo describes where the server runs and whether settings can be
// persisted to disk and the process restarted by the server itself.
type DeploymentInfo struct {
	Type         string `json:"type"`
	ConfigSource string `json:"config_source"`
	ConfigFormat string `json:"config_format"`
	CanPersist   bool   `json:"can_persist"`
	CanRestart   bool   `json:"can_restart"`
	// ConfigBackend names the effective persistence backend: "file",
	// "kubernetes-secret" or empty when the config is not persistable.
	ConfigBackend string `json:"config_backend,omitempty"`
	// RemoteConfigured is true when a remote (kubernetes Secret) config target
	// is available in addition to the local file.
	RemoteConfigured bool `json:"remote_configured,omitempty"`
	// RemoteTarget is the resolved remote backing store description when one is
	// configured.
	RemoteTarget string `json:"remote_target,omitempty"`
	// CanReload is true when persisting the config triggers a workload reload
	// (e.g. a kubernetes deployment rollout) even though the server cannot
	// restart itself via an OS service manager.
	CanReload bool     `json:"can_reload,omitempty"`
	Reasons   []string `json:"reasons,omitempty"`
}

func envDeploymentType() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("OPEN_CHAT_DEPLOYMENT_TYPE")))
}

func detectDeploymentType() string {
	if explicit := envDeploymentType(); explicit != "" {
		return explicit
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "container"
	}
	if installed, _, err := servicecontrol.Status(); err == nil && installed {
		return "service"
	}
	return "binary"
}

// DetectConfigFormat classifies a resolved config source. It returns one of
// "json", "yaml", "inline", or "none".
func DetectConfigFormat(source string) string {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return "none"
	}
	if strings.HasPrefix(trimmed, "inline") {
		return "inline"
	}
	content, err := os.ReadFile(trimmed)
	if err != nil {
		return "unknown"
	}
	trimmedContent := bytes.TrimSpace(content)
	if len(trimmedContent) == 0 {
		return "json"
	}
	if json.Valid(trimmedContent) {
		return "json"
	}
	return "yaml"
}

// BuildDeploymentInfo inspects the environment and returns the deployment
// capabilities surfaced to the settings UI.
func BuildDeploymentInfo() DeploymentInfo {
	source := runtimecfg.GetConfigSource()
	format := DetectConfigFormat(source)

	info := DeploymentInfo{
		Type:         detectDeploymentType(),
		ConfigSource: source,
		ConfigFormat: format,
	}

	if (format == "json" || format == "yaml") && strings.TrimSpace(source) != "" && !strings.HasPrefix(strings.TrimSpace(source), "inline") {
		if writableConfigSource(source) {
			info.CanPersist = true
		} else {
			info.Reasons = append(info.Reasons, "config file is not writable")
		}
	} else {
		switch format {
		case "inline":
			info.Reasons = append(info.Reasons, "inline config cannot be persisted")
		case "none":
			info.Reasons = append(info.Reasons, "no config file was provided; start with --config <path> to persist changes")
		case "unknown":
			info.Reasons = append(info.Reasons, "config file could not be read")
		default:
			info.Reasons = append(info.Reasons, "config format is not supported for persistence")
		}
	}

	info.CanRestart = servicecontrol.RestartSupported()
	restartReason := ""
	if !info.CanRestart {
		restartReason = "server is not managed by an OS service manager; restart manually"
	}

	// A registered remote persister (kubernetes deployment-host config secret)
	// makes the config persistable even when the local file is read-only (for
	// example a read-only Secret volume mount), and is the preferred backend.
	// When the integration exposes a live status resolver we use the actually
	// resolved target instead of merely "a persister is registered".
	if status, hasStatus := RemoteConfigStatusSnapshot(); hasStatus {
		info.RemoteConfigured = status.Configured
		if status.Configured {
			info.CanPersist = true
			info.ConfigBackend = "kubernetes-secret"
			info.RemoteTarget = status.Target
			info.Reasons = append(info.Reasons, "config is persisted to the deployment-host kubernetes Secret")
		}
		if status.Reload {
			info.CanReload = true
			info.CanRestart = true
			restartReason = ""
		}
	} else if HasRemoteConfigPersister() {
		info.RemoteConfigured = true
		info.CanPersist = true
		info.ConfigBackend = "kubernetes-secret"
		info.Reasons = append(info.Reasons, "config is persisted to the deployment-host kubernetes Secret")
	} else if info.CanPersist {
		info.ConfigBackend = "file"
	}

	if restartReason != "" {
		info.Reasons = append(info.Reasons, restartReason)
	}

	return info
}

func writableConfigSource(source string) bool {
	info, err := os.Stat(source)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0200 != 0
}
