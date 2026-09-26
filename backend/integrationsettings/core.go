package integrationsettings

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// CoreSettingsName is the synthetic settings group that surfaces the backend
// core's own configuration (effective environment values plus the bootstrap
// specs) in the admin integrations settings UI. The core is not a compiled
// integration, so it never appears in the regular integrations list.
const CoreSettingsName = "core"

// coreBootstrapSections maps the synthetic BOOTSTRAP_* field keys to the
// matching subsection under the config's top-level `bootstrap` object.
var coreBootstrapSections = map[string]string{
	"BOOTSTRAP_USERS":    "users",
	"BOOTSTRAP_BOTS":     "bots",
	"BOOTSTRAP_SSH":      "ssh",
	"BOOTSTRAP_OPENCODE": "opencode",
	"BOOTSTRAP_GIT":      "git",
	"BOOTSTRAP_MCP":      "mcp",
}

var coreBootstrapDescriptions = map[string]string{
	"BOOTSTRAP_USERS":    "Bootstrap user specs applied on startup (JSON).",
	"BOOTSTRAP_BOTS":     "Bootstrap bot specs applied on startup (JSON).",
	"BOOTSTRAP_SSH":      "Bootstrap SSH default owners, keys, servers and grants (JSON).",
	"BOOTSTRAP_OPENCODE": "Bootstrap opencode default owners and projects (JSON).",
	"BOOTSTRAP_GIT":      "Bootstrap git default owners, tokens, repositories, workspaces and triggers (JSON).",
	"BOOTSTRAP_MCP":      "Bootstrap MCP default owners and servers (JSON).",
}

// coreEnvDescriptions provides human readable descriptions for known global
// environment keys. Unknown keys fall back to an empty description.
var coreEnvDescriptions = map[string]string{
	"DB_BACKEND":              "Database backend used by the server (sqlite/postgres).",
	"DB_PATH":                 "Path to the SQLite database file.",
	"DEBUG":                   "Enable debug logging.",
	"HOST":                    "Address the server binds to.",
	"PORT":                    "Port the server listens on.",
	"ROOT_CREDENTIALS":        "Credentials for the initial root/admin user.",
	"DEFAULT_BOT_CREDENTIALS": "Credentials for the default bot user.",
	"CORS_ALLOWED_ORIGINS":    "Comma-separated list of allowed CORS origins.",
	"PUBLIC_BASE_URL":         "Public base URL of this deployment.",
	"REDIS_URL":               "Redis connection URL.",
	"REDIS_MODE":              "Redis mode (embedded/external).",
	"REDIS_ADDR":              "Redis address.",
	"REDIS_PASSWORD":          "Redis password.",
	"REDIS_DB":                "Redis database index.",

	"OPENROUTER_API_KEY":          "OpenRouter API key (openrouter model provider).",
	"DEEPINFRA_API_KEY":           "DeepInfra API key (deepinfra model provider).",
	"LITELLM_API_KEY":             "LiteLLM API key (litellm model provider).",
	"LITELLM_API_HOST":            "LiteLLM API base URL.",
	"OPENAI_API_KEY":              "OpenAI API key (openai model provider).",
	"ANTHROPIC_API_KEY":           "Anthropic API key (anthropic model provider).",
	"ANTHROPIC_API_HOST":          "Anthropic API base URL override.",
	"IONOS_API_KEY":               "Ionos API key (ionos model provider).",
	"GROQ_API_KEY":                "Groq API key (groq model provider).",
	"MSGMATE_CLUSTER_API_KEY":     "Msgmate cluster API key (msgmate_cluster model provider).",
	"MSGMATE_CLUSTER_HOST":        "Msgmate cluster API base URL.",
	"OPEN_CHAT_SEAL_KEY":          "Key used to seal/encrypt stored secrets.",
	"OPEN_CHAT_GITHUB_POST_TOKEN": "GitHub token used by the server for posting comments (CI bots).",
	"OPEN_CHAT_DEPLOYMENT_TYPE":   "Explicit deployment type reported in the UI (managed/container/service/binary).",
}

// coreBootstrapSectionForKey returns the bootstrap subsection for a synthetic
// BOOTSTRAP_* field key.
func coreBootstrapSectionForKey(key string) (string, bool) {
	section, ok := coreBootstrapSections[normalizeKey(key)]
	return section, ok
}

// isCoreBootstrapKey reports whether a field key is one of the synthetic
// bootstrap keys.
func isCoreBootstrapKey(key string) bool {
	_, ok := coreBootstrapSectionForKey(key)
	return ok
}

// CoreDefinition builds the synthetic settings definition for the backend core
// at request time. It exposes one field per effective runtime environment value
// plus the fixed bootstrap subsections.
func CoreDefinition() integrationinterface.Definition {
	values := runtimecfg.GetAll()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, normalizeKey(key))
	}
	sort.Strings(keys)

	envVars := make([]integrationinterface.RuntimeEnvVar, 0, len(keys)+len(coreBootstrapSections))
	for _, key := range keys {
		envVars = append(envVars, integrationinterface.RuntimeEnvVar{
			Key:         key,
			Sensitive:   values[key].Sensitive,
			Description: coreEnvDescriptions[key],
		})
	}

	bootstrapKeys := make([]string, 0, len(coreBootstrapSections))
	for key := range coreBootstrapSections {
		bootstrapKeys = append(bootstrapKeys, key)
	}
	sort.Strings(bootstrapKeys)
	for _, key := range bootstrapKeys {
		envVars = append(envVars, integrationinterface.RuntimeEnvVar{
			Key:         key,
			Description: coreBootstrapDescriptions[key],
		})
	}

	return integrationinterface.Definition{
		Name:           CoreSettingsName,
		AdminOnly:      true,
		RuntimeEnvVars: envVars,
	}
}

// LoadCoreBootstrapValues returns the current bootstrap subsections encoded as
// JSON keyed by their synthetic BOOTSTRAP_* field key. It reads the active
// config file when one is available and falls back to synthesizing the values
// from the runtime bootstrap state otherwise.
func LoadCoreBootstrapValues(source string) map[string]string {
	if values, ok := loadCoreBootstrapFromConfig(source); ok {
		return values
	}
	return synthesizeCoreBootstrapValues()
}

func loadCoreBootstrapFromConfig(source string) (map[string]string, bool) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" || strings.HasPrefix(trimmed, "inline") {
		return nil, false
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, false
	}
	root, _, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, false
	}

	out := map[string]string{}
	bootstrap, _ := root["bootstrap"].(map[string]interface{})
	if bootstrap == nil {
		return out, true
	}
	for key, section := range coreBootstrapSections {
		subsection, ok := bootstrap[section]
		if !ok || subsection == nil {
			continue
		}
		encoded, err := json.Marshal(subsection)
		if err != nil {
			continue
		}
		out[key] = string(encoded)
	}
	return out, true
}

// synthesizeCoreBootstrapValues reconstructs the bootstrap subsections from the
// runtime bootstrap state (used when the configuration was provided inline or
// through flags rather than a readable file).
func synthesizeCoreBootstrapValues() map[string]string {
	bootstrap := runtimecfg.GetOpenChatBootstrap()
	out := map[string]string{}

	if value, ok := marshalSpecs(bootstrap.UserSpecs); ok {
		out["BOOTSTRAP_USERS"] = value
	}
	if value, ok := marshalSpecs(bootstrap.BotSpecs); ok {
		out["BOOTSTRAP_BOTS"] = value
	}

	ssh := map[string]interface{}{}
	if len(bootstrap.SSHDefaultOwners) > 0 {
		ssh["owners"] = bootstrap.SSHDefaultOwners
	}
	if value, ok := parseSpecs(bootstrap.SSHKeySpecs); ok {
		ssh["keys"] = value
	}
	if value, ok := parseSpecs(bootstrap.SSHServerSpecs); ok {
		ssh["servers"] = value
	}
	if value, ok := parseSpecs(bootstrap.SSHKeyGrantSpecs); ok {
		ssh["key_grants"] = value
	}
	if value, ok := parseSpecs(bootstrap.SSHServerGrantSpecs); ok {
		ssh["server_grants"] = value
	}
	if value, ok := marshalObject(ssh); ok {
		out["BOOTSTRAP_SSH"] = value
	}

	opencode := map[string]interface{}{}
	if len(bootstrap.OpencodeDefaultOwners) > 0 {
		opencode["owners"] = bootstrap.OpencodeDefaultOwners
	}
	if value, ok := parseSpecs(bootstrap.OpencodeProjectSpecs); ok {
		opencode["projects"] = value
	}
	if value, ok := marshalObject(opencode); ok {
		out["BOOTSTRAP_OPENCODE"] = value
	}

	git := map[string]interface{}{}
	if len(bootstrap.GitDefaultOwners) > 0 {
		git["owners"] = bootstrap.GitDefaultOwners
	}
	if value, ok := parseSpecs(bootstrap.GitTokenSpecs); ok {
		git["tokens"] = value
	}
	if value, ok := parseSpecs(bootstrap.GitRepositorySpecs); ok {
		git["repositories"] = value
	}
	if value, ok := parseSpecs(bootstrap.GitWorkspaceSpecs); ok {
		git["workspaces"] = value
	}
	if value, ok := parseSpecs(bootstrap.GitWorkspaceGrantSpecs); ok {
		git["workspace_grants"] = value
	}
	if value, ok := parseSpecs(bootstrap.GitTriggerSpecs); ok {
		git["triggers"] = value
	}
	if value, ok := marshalObject(git); ok {
		out["BOOTSTRAP_GIT"] = value
	}

	mcp := map[string]interface{}{}
	if len(bootstrap.MCPDefaultOwners) > 0 {
		mcp["owners"] = bootstrap.MCPDefaultOwners
	}
	if value, ok := parseSpecs(bootstrap.MCPServerSpecs); ok {
		mcp["servers"] = value
	}
	if value, ok := marshalObject(mcp); ok {
		out["BOOTSTRAP_MCP"] = value
	}

	return out
}

// parseSpecs decodes the raw bootstrap specs into a JSON-friendly value. A
// single spec is returned as-is (object or array), multiple specs are combined
// into an array.
func parseSpecs(specs []string) (interface{}, bool) {
	trimmed := make([]string, 0, len(specs))
	for _, spec := range specs {
		if value := strings.TrimSpace(spec); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	if len(trimmed) == 0 {
		return nil, false
	}
	if len(trimmed) == 1 {
		var parsed interface{}
		if err := json.Unmarshal([]byte(trimmed[0]), &parsed); err == nil {
			return parsed, true
		}
		return trimmed[0], true
	}
	parsed := make([]interface{}, 0, len(trimmed))
	for _, spec := range trimmed {
		var item interface{}
		if err := json.Unmarshal([]byte(spec), &item); err != nil {
			item = spec
		}
		parsed = append(parsed, item)
	}
	return parsed, true
}

func marshalSpecs(specs []string) (string, bool) {
	value, ok := parseSpecs(specs)
	if !ok {
		return "", false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func marshalObject(object map[string]interface{}) (string, bool) {
	if len(object) == 0 {
		return "", false
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}
