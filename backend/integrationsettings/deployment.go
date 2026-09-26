package integrationsettings

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"backend/runtimecfg"
	"backend/servicecontrol"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// DeploymentSettingsName is the synthetic settings group that surfaces global
// deployment environment values (model provider keys, hosts, ...) in the admin
// integrations settings UI. Values saved for it are written to the config's
// global `env` section.
const DeploymentSettingsName = "deployment"

// deploymentEnvVars lists the global env keys exposed for editing. They are
// intentionally not OCI_-prefixed (they are deployment-wide provider/runtime
// values rather than integration-scoped settings).
func deploymentEnvVars() []integrationinterface.RuntimeEnvVar {
	return []integrationinterface.RuntimeEnvVar{
		{Key: "OPENROUTER_API_KEY", Sensitive: true, Description: "OpenRouter API key (openrouter model provider)."},
		{Key: "DEEPINFRA_API_KEY", Sensitive: true, Description: "DeepInfra API key (deepinfra model provider)."},
		{Key: "LITELLM_API_KEY", Sensitive: true, Description: "LiteLLM API key (litellm model provider)."},
		{Key: "LITELLM_API_HOST", Sensitive: false, Description: "LiteLLM API base URL."},
		{Key: "OPENAI_API_KEY", Sensitive: true, Description: "OpenAI API key (openai model provider)."},
		{Key: "ANTHROPIC_API_KEY", Sensitive: true, Description: "Anthropic API key (anthropic model provider)."},
		{Key: "ANTHROPIC_API_HOST", Sensitive: false, Description: "Anthropic API base URL override."},
		{Key: "IONOS_API_KEY", Sensitive: true, Description: "Ionos API key (ionos model provider)."},
		{Key: "GROQ_API_KEY", Sensitive: true, Description: "Groq API key (groq model provider)."},
		{Key: "MSGMATE_CLUSTER_API_KEY", Sensitive: true, Description: "Msgmate cluster API key (msgmate_cluster model provider)."},
		{Key: "MSGMATE_CLUSTER_HOST", Sensitive: false, Description: "Msgmate cluster API base URL."},
		{Key: "OPEN_CHAT_GITHUB_POST_TOKEN", Sensitive: true, Description: "GitHub token used by the server for posting comments (CI bots)."},
		{Key: "CORS_ALLOWED_ORIGINS", Sensitive: false, Description: "Comma-separated list of allowed CORS origins."},
		{Key: "PUBLIC_BASE_URL", Sensitive: false, Description: "Public base URL of this deployment."},
	}
}

// DeploymentDefinition returns the synthetic definition backing the deployment
// settings group.
func DeploymentDefinition() integrationinterface.Definition {
	return integrationinterface.Definition{
		Name:           DeploymentSettingsName,
		AdminOnly:      true,
		RuntimeEnvVars: deploymentEnvVars(),
	}
}

// DeploymentInfo describes where the server runs and whether settings can be
// persisted to disk and the process restarted by the server itself.
type DeploymentInfo struct {
	Type         string   `json:"type"`
	ConfigSource string   `json:"config_source"`
	ConfigFormat string   `json:"config_format"`
	CanPersist   bool     `json:"can_persist"`
	CanRestart   bool     `json:"can_restart"`
	Reasons      []string `json:"reasons,omitempty"`
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
	if !info.CanRestart {
		info.Reasons = append(info.Reasons, "server is not managed by an OS service manager; restart manually")
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
