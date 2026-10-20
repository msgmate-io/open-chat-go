package nativecoder

import (
	"os"
	"strings"
)

// This file owns the per-repo development set-up plan (ci#756): a chat
// interaction can declare `coder_dev_setup` in its shared config (a profile
// name or a map) and core materializes the matching shell into the existing
// `coder_sandbox_setup_commands` key. The coder integration hands that key to
// the managed sandbox provider, which runs it inside the freshly provisioned
// per-chat sandbox BEFORE the first LLM turn.
//
// That is what keeps a development agent from discovering a half-set-up
// checkout: the plan pulls the repository's submodules and bootstraps the
// integration build tooling (`openchat-integrations prepare --profile <p>`,
// see profile_setup.yaml / DEVELOPMENT.md), so helm, ci and mobile profiles
// arrive pre-materialized instead of being installed ad hoc by the model.
//
// Defaults are per repository (DefaultDevSetupProfiles): a known development
// repo gets its profile automatically, an unknown repo only when its agent
// config declares one. OCI_MSGMATE_CODER_DEV_SETUP=0 rolls the per-repo
// default back fleet-wide, and an explicit `coder_dev_setup` block always wins.
const (
	// DevSetupConfigKey is the bot/interaction shared-config key that declares
	// the development set-up plan (string profile or map, see DevSetupSpec).
	DevSetupConfigKey = "coder_dev_setup"
	// SandboxSetupCommandsConfigKey is the shared-config key the coder
	// integration reads; the script stored here runs inside the sandbox after
	// the clone and before the agent's first turn.
	SandboxSetupCommandsConfigKey = "coder_sandbox_setup_commands"
	// DevSetupEnvKey is the deployment-wide kill switch for the per-repo default.
	DevSetupEnvKey = "OCI_MSGMATE_CODER_DEV_SETUP"
	// DevSetupProfileDefault is the profile of the built-in default for a
	// known development repository (all batteries included, ci#756).
	DevSetupProfileDefault = "full-development"
	// devSetupSetupMarker is prepended to every generated script so the sandbox
	// transcript shows which profile prepared the workspace.
	devSetupSetupMarker = "[ci#756 dev-setup]"
)

// DevSetupRepoProfile maps a repository prefix to the integration profile its
// checkouts need. The first (longest) match wins, so a more specific entry
// overrides a broader one.
type DevSetupRepoProfile struct {
	RepoPrefix string
	Profile    string
}

// DefaultDevSetupProfiles is the per-repo default plan: every msgmate-io
// development repository that needs submodules and the build tooling.
var DefaultDevSetupProfiles = []DevSetupRepoProfile{
	{RepoPrefix: "msgmate-io/open-chat-go-mobile", Profile: "full-android"},
	{RepoPrefix: "msgmate-io/open-chat-go-ci", Profile: DevSetupProfileDefault},
	{RepoPrefix: "msgmate-io/open-chat-go-helm-chart", Profile: "full-ci"},
	{RepoPrefix: "msgmate-io/open-chat-go-llm-coding-context", Profile: "full-ci"},
	{RepoPrefix: "msgmate-io/open-chat-go-build-tools", Profile: "core-only"},
	{RepoPrefix: "msgmate-io/open-chat-go", Profile: DevSetupProfileDefault},
}

// DevSetupSpec is a resolved development set-up plan.
type DevSetupSpec struct {
	// Enabled reports whether a plan exists at all.
	Enabled bool
	// Profile is the integration profile to materialize (`full-ci`, …).
	Profile string
	// Submodules pulls the repository's git submodules.
	Submodules bool
	// ExtraRepos are additional repositories cloned next to the checkout.
	ExtraRepos []string
	// Commands are extra shell lines appended verbatim to the script.
	Commands []string
}

// DevSetupSpecForRepo returns the built-in plan for a repository, or
// Enabled=false when the repository is not a known development repository.
func DevSetupSpecForRepo(repo string) DevSetupSpec {
	normalized := NormalizeRepo(repo)
	if normalized == "" {
		return DevSetupSpec{}
	}
	best := DevSetupSpec{}
	bestLen := -1
	for _, entry := range DefaultDevSetupProfiles {
		if !strings.HasPrefix(normalized, entry.RepoPrefix) {
			continue
		}
		if len(entry.RepoPrefix) > bestLen {
			bestLen = len(entry.RepoPrefix)
			best = DevSetupSpec{Enabled: true, Profile: entry.Profile, Submodules: true}
		}
	}
	return best
}

// ResolveDevSetup reads an explicit `coder_dev_setup` declaration. The string
// form is the profile name, the map form accepts `enabled`, `profile`,
// `submodules`, `repos` and `commands`. It reports false when the config
// declares no plan or switches it off.
func ResolveDevSetup(config map[string]interface{}) (DevSetupSpec, bool) {
	if config == nil {
		return DevSetupSpec{}, false
	}
	raw, exists := config[DevSetupConfigKey]
	if !exists {
		return DevSetupSpec{}, false
	}
	switch typed := raw.(type) {
	case string:
		profile := normalizeDevSetupProfile(typed)
		if profile == "" {
			return DevSetupSpec{}, false
		}
		return DevSetupSpec{Enabled: true, Profile: profile, Submodules: true}, true
	case bool:
		if !typed {
			return DevSetupSpec{}, false
		}
		return DevSetupSpec{Enabled: true, Profile: DevSetupProfileDefault, Submodules: true}, true
	case map[string]interface{}:
		if !devSetupBool(typed["enabled"], true) {
			return DevSetupSpec{}, false
		}
		spec := DevSetupSpec{
			Enabled:    true,
			Profile:    normalizeDevSetupProfile(firstString(typed, "profile", "integration_profile")),
			Submodules: devSetupBool(typed["submodules"], true),
			ExtraRepos: normalizedRepos(stringSliceFromConfig(firstValue(typed, "repos", "repositories"))),
			Commands:   trimmedLines(stringSliceFromConfig(typed["commands"])),
		}
		if spec.Profile == "" {
			spec.Profile = DevSetupProfileDefault
		}
		return spec, true
	default:
		return DevSetupSpec{}, false
	}
}

// DevSetupScript renders the shell a sandbox runs to prepare the checkout. It
// is empty for a disabled plan. The script is idempotent and non-fatal about
// absent optional tooling (no python3, no build-tools checkout), but any real
// failure of a command it does run fails the script, so an unprepared
// workspace is reported instead of silently handed to the model.
func DevSetupScript(spec DevSetupSpec) string {
	if !spec.Enabled {
		return ""
	}
	var lines []string
	lines = append(lines, devSetupSetupMarker+" profile "+spec.Profile)
	if spec.Submodules {
		lines = append(lines, `if [ -f .gitmodules ]; then git submodule update --init --recursive; fi`)
	}
	for _, repo := range spec.ExtraRepos {
		name := devSetupRepoDir(repo)
		if name == "" {
			continue
		}
		lines = append(lines, `[ -d `+name+` ] || git clone --depth 1 https://github.com/`+repo+`.git `+name)
	}
	lines = append(lines,
		`if [ -d development/build-tools ] && command -v python3 >/dev/null 2>&1; then`,
		`  [ -d .venv ] || python3 -m venv .venv`,
		`  .venv/bin/pip install --quiet --disable-pip-version-check ./development/build-tools`,
		`  .venv/bin/openchat-integrations prepare --profile `+spec.Profile,
		`fi`,
	)
	lines = append(lines, spec.Commands...)
	return "set -e\n" + strings.Join(lines, "\n") + "\n"
}

// WithDevSetup returns config whose `coder_sandbox_setup_commands` carries the
// resolved development set-up script, unless the agent already configured that
// key itself (an explicit script always wins). repo is the repository the
// interaction works on; an empty value is taken from the config's repo keys.
// envValue is the raw value of DevSetupEnvKey: an empty value keeps the
// per-repo default, a false-like value rolls it back fleet-wide. The input map
// is never mutated and non-coder configs are returned unchanged.
func WithDevSetup(config map[string]interface{}, repo string, envValue string) map[string]interface{} {
	if config == nil {
		return nil
	}
	if _, exists := config[SandboxSetupCommandsConfigKey]; exists {
		return config
	}
	spec, explicit := ResolveDevSetup(config)
	if !explicit {
		if devSetupEnvDisabled(envValue) || !IsCoderAgentConfig(config) {
			return config
		}
		if strings.TrimSpace(repo) == "" {
			repo = repoFromConfig(config)
		}
		spec = DevSetupSpecForRepo(repo)
	}
	script := DevSetupScript(spec)
	if script == "" {
		return config
	}
	out := make(map[string]interface{}, len(config)+1)
	for key, value := range config {
		out[key] = value
	}
	out[SandboxSetupCommandsConfigKey] = script
	return out
}

// DevSetupEnvValue returns the deployment-wide kill-switch value.
func DevSetupEnvValue() string {
	return os.Getenv(DevSetupEnvKey)
}

// devSetupEnvDisabled reports whether a kill-switch value means "off".
func devSetupEnvDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "off", "no", "disabled":
		return true
	default:
		return false
	}
}

// normalizeDevSetupProfile accepts a profile name only in the charset the
// build tooling uses, so a config value can never inject shell syntax.
func normalizeDevSetupProfile(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > 64 {
		return ""
	}
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return ""
		}
	}
	return trimmed
}

// devSetupRepoDir returns the checkout directory name for an extra repository,
// or "" when the repository is not safe to clone into the workspace.
func devSetupRepoDir(repo string) string {
	normalized := NormalizeRepo(repo)
	if normalized == "" {
		return ""
	}
	return devSetupRepoDirName(normalized)
}

func devSetupRepoDirName(normalized string) string {
	parts := strings.Split(normalized, "/")
	name := parts[len(parts)-1]
	if normalizeDevSetupProfile(name) == "" {
		return ""
	}
	return name
}

// normalizedRepos normalizes a list of repositories, dropping unusable entries.
func normalizedRepos(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if repo := NormalizeRepo(value); repo != "" {
			out = append(out, repo)
		}
	}
	return out
}

func trimmedLines(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstValue(config map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := config[key]; ok {
			return value
		}
	}
	return nil
}

func firstString(config map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := config[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// devSetupBool reads a boolean that may be stored as a bool or as one of the
// common boolean-like strings.
func devSetupBool(raw interface{}, fallback bool) bool {
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "false", "0", "no", "off", "disabled":
			return false
		case "true", "1", "yes", "on", "enabled":
			return true
		}
	}
	return fallback
}

// DevSetupRepoForInteraction resolves the repository an interaction's
// development set-up runs against. It follows the same precedence as the
// automatic per-chat sandbox checkout: a repository the caller pinned for this
// interaction (tool_init.coder_select_workspace.git_repo or a repo key in
// config_overrides) is authoritative, otherwise an explicit `repo:<...>` tag
// wins, falling back to the effective config's repo key (the bot/trigger
// default). The result is a normalized `owner/name` slug, or "" when no
// repository is known - in that case only repo-agnostic bootstrap commands run.
func DevSetupRepoForInteraction(config map[string]interface{}, tags []string, toolInit, configOverrides map[string]interface{}) string {
	if repo := ExplicitAutoSandboxRepo(toolInit, configOverrides); repo != "" {
		return NormalizeRepo(repo)
	}
	return NormalizeRepo(AutoSandboxRepoTarget(config, tags))
}
