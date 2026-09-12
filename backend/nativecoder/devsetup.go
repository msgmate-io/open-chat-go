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
	// known development repository (all batteries included, ci#756): the
	// `full-development` profile materializes the private CI tooling, the Helm
	// chart, the mobile client and the LLM coding context in one go.
	DevSetupProfileDefault = "full-development"
	// DevSetupProfileFallback is the profile the generated set-up script falls
	// back to when the private integrations manifest does not know
	// DevSetupProfileDefault yet: the private profile membership ships from the
	// `ci` repository, so the public default must degrade gracefully instead of
	// failing every coding sandbox with an unknown-profile error.
	DevSetupProfileFallback = "full-ci"
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
	{RepoPrefix: "msgmate-io/open-chat-go-ci", Profile: "full-ci"},
	{RepoPrefix: "msgmate-io/open-chat-go-helm-chart", Profile: "full-ci"},
	{RepoPrefix: "msgmate-io/open-chat-go-llm-coding-context", Profile: "full-ci"},
	{RepoPrefix: "msgmate-io/open-chat-go-build-tools", Profile: "core-only"},
	// ci#756: every repository the sandbox palette maps a ci.msgmate.io
	// git-repo trigger to needs a per-repo default as well, otherwise a bot or
	// trigger pointed at one of them starts half-set-up (bare checkout, no
	// submodules, no build tooling) and the model installs tooling ad hoc
	// instead of receiving a prepared checkout before its first turn. The
	// profile follows the use-case of the repository, see the comments below.
	// The frontend repository builds backend and frontend together (`full`:
	// full integrations manifest, no Android client).
	{RepoPrefix: "msgmate-io/open-chat-go-frontend", Profile: "full"},
	// The Android host app needs the mobile bindings and the CI tooling that
	// carries the client build scripts.
	{RepoPrefix: "msgmate-io/open-chat-go-mobile-client", Profile: "full-android"},
	// Go integration/tool repositories only need the private integrations
	// manifest; Helm/mobile/LLM checkouts would be dead weight.
	{RepoPrefix: "msgmate-io/open-chat-go-mcp-integration", Profile: "default"},
	{RepoPrefix: "msgmate-io/open-chat-go-rest-api-tool", Profile: "default"},
	{RepoPrefix: "msgmate-io/open-chat-go-go-client", Profile: "default"},
	// The Odoo stack and the VNC desktop client are verified through their own
	// sandbox variants; their checkouts need the private CI tooling and the
	// deployment manifests, not the Android client.
	{RepoPrefix: "msgmate-io/open-chat-go-odoo-integration", Profile: "full-ci"},
	{RepoPrefix: "msgmate-io/open-chat-go-vnc-client", Profile: "full-ci"},
	// The nix toolchain repository is public and needs no private checkout at
	// all; it only has to get the core set-up instead of being treated as an
	// unknown repository (tbscode/tims-nix#10).
	{RepoPrefix: "tbscode/tims-nix", Profile: "core-only"},
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
	// Profiles overrides Profile per repository prefix. It is the per-repo
	// knob of the interaction-configurable plan: one bot (or trigger) that
	// serves several repositories can declare the profile each repository
	// needs without forcing every interaction onto the same set-up. The
	// longest matching prefix wins, exactly like DefaultDevSetupProfiles.
	Profiles []DevSetupRepoProfile
	// SetupCommands overrides the generated script content entirely: an explicit
	// non-empty list of shell lines is appended verbatim as the set-up body.
	// It is the interaction-configurable escape hatch for repositories whose
	// set-up cannot be expressed as an integration profile — the operator keeps
	// full control of the command text, so nothing is auto-generated and
	// nothing is guessed. Profile, Submodules, ExtraRepos and Commands are
	// ignored while SetupCommands is set.
	SetupCommands []string
}

// forRepo returns the spec with the profile its per-repo table selects for
// repo. A repository outside the table keeps the plan's default profile, so
// declaring a per-repo table never disables the set-up for the other repos the
// same configuration is dispatched for.
func (spec DevSetupSpec) forRepo(repo string) DevSetupSpec {
	normalized := NormalizeRepo(repo)
	if normalized == "" || len(spec.Profiles) == 0 {
		return spec
	}
	best := ""
	bestLen := -1
	for _, entry := range spec.Profiles {
		if !strings.HasPrefix(normalized, entry.RepoPrefix) {
			continue
		}
		if len(entry.RepoPrefix) > bestLen {
			bestLen, best = len(entry.RepoPrefix), entry.Profile
		}
	}
	if best != "" {
		spec.Profile = best
	}
	return spec
}

// devSetupProfileOverrides reads the `profiles` map of an explicit
// `coder_dev_setup` block (`{<repo-prefix>: <profile>}`). Both a JSON-decoded
// `map[string]interface{}` and a literal `map[string]string` are accepted;
// entries with an unusable prefix or profile are dropped rather than turned
// into shell input.
func devSetupProfileOverrides(raw interface{}) []DevSetupRepoProfile {
	entries := map[string]string{}
	switch typed := raw.(type) {
	case map[string]interface{}:
		for repo, value := range typed {
			if profile, ok := value.(string); ok {
				entries[repo] = profile
			}
		}
	case map[string]string:
		for repo, profile := range typed {
			entries[repo] = profile
		}
	}
	out := make([]DevSetupRepoProfile, 0, len(entries))
	for repo, profile := range entries {
		normalized := NormalizeRepo(repo)
		if normalized == "" {
			continue
		}
		if normalizedProfile := normalizeDevSetupProfile(profile); normalizedProfile != "" {
			out = append(out, DevSetupRepoProfile{RepoPrefix: normalized, Profile: normalizedProfile})
		}
	}
	return out
}

// DevSetupRepoEntryFor returns the per-repo default entry that matches repo
// most specifically, mirroring the precedence of DevSetupSpecForRepo. It lets
// the audit tell an explicit per-repo declaration apart from the broad monorepo
// fallback: a ci.msgmate.io git-repo trigger for a repository whose development
// process differs from the monorepo (a client, an integration, a toolchain
// repository) must carry its own entry instead of inheriting the monorepo's
// batteries-included profile.
func DevSetupRepoEntryFor(repo string) (DevSetupRepoProfile, bool) {
	normalized := NormalizeRepo(repo)
	if normalized == "" {
		return DevSetupRepoProfile{}, false
	}
	best := DevSetupRepoProfile{}
	bestLen := -1
	for _, entry := range DefaultDevSetupProfiles {
		if !strings.HasPrefix(normalized, entry.RepoPrefix) {
			continue
		}
		if len(entry.RepoPrefix) > bestLen {
			bestLen, best = len(entry.RepoPrefix), entry
		}
	}
	if bestLen < 0 {
		return DevSetupRepoProfile{}, false
	}
	return best, true
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
			Enabled:       true,
			Profile:       normalizeDevSetupProfile(firstString(typed, "profile", "integration_profile")),
			Submodules:    devSetupBool(typed["submodules"], true),
			ExtraRepos:    normalizedRepos(stringSliceFromConfig(firstValue(typed, "repos", "repositories"))),
			Commands:      trimmedLines(stringSliceFromConfig(typed["commands"])),
			Profiles:      devSetupProfileOverrides(firstValue(typed, "profiles", "per_repo_profiles", "repo_profiles")),
			SetupCommands: trimmedLines(stringSliceFromConfig(typed["setup_commands"])),
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
	// ci#739: the set-up shell and the agent's later tool-call shells are
	// different processes, so the toolchain PATH and the private-module opt-outs
	// must be persisted, not merely exported here (see SandboxEnvSetupLines).
	lines = append(lines, SandboxEnvSetupLines()...)
	if len(spec.SetupCommands) > 0 {
		// ci#756: an operator-provided `setup_commands` body replaces the
		// generated profile/submodule bootstrap entirely — the operator keeps
		// full control of the command text, so nothing is auto-generated and
		// nothing is guessed. The environment persistence above still applies
		// and the marker keeps the transcript readable.
		lines = append(lines, spec.SetupCommands...)
		return "set -e\n" + strings.Join(lines, "\n") + "\n"
	}
	if spec.Submodules {
		// ci#756: pull the repository's submodules up front so the integration
		// profile set-up below can materialize the checkouts it needs (Helm,
		// mobile client). The recursive update is guarded on `.gitmodules` so a
		// submodule-absorbed revision does not make the sandbox fail; `git`
		// may be absent in the freshly-cloned image, in which case the profile
		// set-up reports the gap instead of silently handing over a bare
		// checkout.
		lines = append(lines,
			`if [ -f .gitmodules ] && command -v git >/dev/null 2>&1; then`,
			`  git submodule update --init --recursive || echo "[ci#756 dev-setup] WARNING: submodule pull failed; continuing with upstream discovery (see build_tools.sh)" >&2`,
			`fi`,
		)
	}
	for _, repo := range spec.ExtraRepos {
		name := devSetupRepoDir(repo)
		if name == "" {
			continue
		}
		lines = append(lines, `[ -d `+name+` ] || git clone --depth 1 https://github.com/`+repo+`.git `+name)
	}
	// ci#739: bootstrap the integration manager through its own entry point.
	// The previous inline `[ -d development/build-tools ]` gate silently skipped
	// provisioning whenever the build-tools submodule was not materialized (the
	// usual state in a fresh checkout), leaving exactly the unprovisioned
	// workspace the agent then reports as a private-module defect. build_tools.sh
	// installs the CLI into .venv on first use and falls back to the upstream
	// repository; a success marker keeps a re-run (re-attached sandbox, replayed
	// set-up) from paying the full provisioning cost again, and the readiness
	// check afterwards tells the transcript whether the workspace can actually run
	// local Go checks.
	lines = append(lines, SandboxProfileSetupLines(spec.Profile)...)
	lines = append(lines,
		`if [ -x development/scripts/check_coder_sandbox_readiness.sh ]; then`,
		`  development/scripts/check_coder_sandbox_readiness.sh . || true`,
		`fi`,
		// Legacy path: no `development/scripts/build_tools.sh` in the checkout
		// (an older revision of the repository), but the vendored build-tools
		// submodule is present, so install the CLI directly. This path is the
		// only remaining user of the submodule, which is why the submodule-free
		// bootstrapping above matters.
		`if [ ! -f "$openchat_sandbox_profile" ] && [ ! -d development/build-tools ] && [ ! -x development/scripts/build_tools.sh ]; then`,
		`  echo "[ci#739 dev-setup] WARNING: neither development/scripts/build_tools.sh nor development/build-tools is present; the checkout cannot be provisioned" >&2`,
		`fi`,
		`if [ ! -f "$openchat_sandbox_profile" ] && [ -d development/build-tools ] && [ ! -x development/scripts/build_tools.sh ] && command -v python3 >/dev/null 2>&1; then`,
		`  [ -d .venv ] || python3 -m venv .venv`,
		`  .venv/bin/pip install --quiet --disable-pip-version-check ./development/build-tools`,
		`  `+devSetupPrepareCommand(`.venv/bin/openchat-integrations`, spec.Profile)+` && printf '%s\n' '`+spec.Profile+`' > "$openchat_sandbox_profile"`,
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
	if devSetupConfiguredScript(config[SandboxSetupCommandsConfigKey]) {
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
	} else {
		// ci#756: an explicit plan may carry a per-repo profile table
		// (`profiles: {<repo-prefix>: <profile>}`) so one bot/interaction
		// config can serve several repositories with different set-ups
		// (eg `full-development` for open-chat-go, `core-only` for a
		// helper repo). The profile of the dispatched repository wins;
		// a repository outside the table keeps the declared default.
		spec = spec.forRepo(repo)
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

// devSetupConfiguredScript reports whether the config already carries an
// operator-provided set-up script. Only a non-empty string counts: an explicit
// but empty/null value (JSON `null`, `""`) is treated as "not configured", so a
// deliberately cleared key does not silently disable the ci#756 preparation.
func devSetupConfiguredScript(value interface{}) bool {
	if value == nil {
		return false
	}
	script, ok := value.(string)
	if !ok {
		// A non-string value is malformed input; refusing to touch it keeps the
		// operator's configuration intact instead of overwriting it.
		return true
	}
	return strings.TrimSpace(script) != ""
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
