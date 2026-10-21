package nativecoder

import "strings"

// ci#739: the per-chat coding sandbox the msgmate coder agents run in ships a
// Go toolchain and the profile-based integration workspace tooling, but none of
// it is wired into the shell the agent drives. Two symptoms recur in the run
// logs:
//
//   - `go` / `gofmt` are installed (the image has /usr/local/go/bin/go, the
//     same entry dev.dockerfile exports) yet absent from PATH, so every local
//     build or unit test starts with a `command not found` and the agent burns
//     turns rediscovering the absolute path.
//   - integration modules are materialized on demand
//     (`openchat-integrations setup/sync --profile …`) and the private ones
//     live in a private repository, so a plain `go test` fails with "module
//     lookup disabled" / "no required module provides package
//     github.com/msgmate-io/…" and the agent misreads it as a code defect
//     instead of an unconfigured workspace.
//
// This file is the single source of truth for the resulting guidance and for
// the environment augmentation of sandbox tool calls. It is pure policy (no
// process spawning), so it is unit-testable without a sandbox.
const (
	// SandboxGoBinDir is where the coding image installs the Go toolchain.
	// dev.dockerfile exports PATH=/dev_bin:/usr/local/go/bin:… (line 9), which
	// is exactly what happens to be missing from the shell the agent drives.
	SandboxGoBinDir = "/usr/local/go/bin"
	// SandboxDevBinDir holds the Go-installed helpers (swag, CompileDaemon).
	SandboxDevBinDir = "/dev_bin"
	// SandboxGoPathBinDir holds `go install`ed binaries of the sandbox user.
	SandboxGoPathBinDir = "/go/bin"
	// SandboxPrivateModuleOrg is the GitHub org owning the private integration
	// modules (and the private profile manifest).
	SandboxPrivateModuleOrg = "github.com/msgmate-io/*"
	// SandboxProfileSetupCommand materializes the integration checkouts, the
	// private manifest mirror, `backend/go.work` and `imports_gen` that the Go
	// workspace and the private modules are resolved from. `backend/go.work` is
	// gitignored, so a fresh sandbox has neither it nor the private checkouts
	// until this runs — that is precisely the failure this policy explains.
	// See DEVELOPMENT.md ("Integration manager", ~line 331).
	SandboxProfileSetupCommand = "development/scripts/build_tools.sh >/dev/null && $(development/scripts/build_tools.sh) prepare --profile full-ci"
)

// SandboxToolchainPathEntries are prepended to PATH for sandbox tool calls so
// the toolchain the image installs is reachable by name. The Go toolchain dir
// comes first (the image's own order puts /dev_bin first); only entries that
// actually exist in the image are listed, so prepending them is a no-op on a
// healthy sandbox.
var SandboxToolchainPathEntries = []string{SandboxGoBinDir, SandboxDevBinDir, SandboxGoPathBinDir}

// SandboxPrivateModuleEnv is the environment a sandbox tool call needs to fetch
// the private integration modules without interactive credentials. Only
// non-secret values: the org pattern, the checksum-db opt-out for it, and
// non-interactive git.
func SandboxPrivateModuleEnv() map[string]string {
	return map[string]string{
		"GOPRIVATE":           SandboxPrivateModuleOrg,
		"GONOSUMDB":           SandboxPrivateModuleOrg,
		"GOFLAGS":             "-mod=mod",
		"GIT_TERMINAL_PROMPT": "0",
	}
}

// SandboxProfileFile persists the profile of the last successful set-up so a
// later shell can tell an already-provisioned workspace from a fresh one. It
// lives inside `.git/` (with a HOME fallback) so the marker never shows up as an
// untracked file in the agent's checkout.
const SandboxProfileFile = ".git/openchat-sandbox-profile"

// sandboxEnvRcMarker guards the rc-file appends, so re-running the set-up is
// idempotent and never grows the shell rc file.
const sandboxEnvRcMarker = "# openchat sandbox environment"

// SandboxEnvSetupLines renders the idempotent, shell-escaping-free lines that
// persist the sandbox toolchain PATH and the private-module opt-outs for the
// *agent's* shells. The dev-setup script runs in its own process, so an
// `export` there is invisible to the later `coder_bash` calls — the recurring
// ci#739 symptom where `go`/`gofmt` are "command not found" and a private module
// fails to resolve even though the set-up itself succeeded. Appending to the
// user's shell rc (and to /etc/profile.d when the sandbox runs as a user that
// can write it) makes the same environment reachable interactively and
// non-interactively; every entry is idempotent, so re-running the set-up does
// not grow the rc file.
func SandboxEnvSetupLines() []string {
	entries := strings.Join(SandboxToolchainPathEntries, ":")
	moduleEnv := SandboxPrivateModuleEnv()
	// Deterministic order so the generated script is byte-stable for tests.
	envKeys := []string{"GOPRIVATE", "GONOSUMDB", "GOFLAGS", "GIT_TERMINAL_PROMPT"}
	var envAssignments []string
	for _, key := range envKeys {
		if value, ok := moduleEnv[key]; ok {
			envAssignments = append(envAssignments, "export "+key+"="+value)
		}
	}
	envLine := strings.Join(envAssignments, "; ")
	return []string{
		`export PATH="` + entries + `:$PATH"`,
		envLine,
		// The export above only lives in this process; persist both for the
		// agent's later shells. `/etc/profile.d` needs root, so a missing write
		// permission is not an error.
		`for rc in "$HOME/.bashrc" "$HOME/.profile"; do`,
		`  [ -f "$rc" ] || touch "$rc" 2>/dev/null || continue`,
		`  if ! grep -qF '` + sandboxEnvRcMarker + `' "$rc" 2>/dev/null; then`,
		`    { echo '` + sandboxEnvRcMarker + `'; echo 'export PATH="` + entries + `:$PATH"'; echo '` + envLine + `'; } >> "$rc" 2>/dev/null || true`,
		`  fi`,
		`done`,
		`if [ -d /etc/profile.d ] && [ -w /etc/profile.d ]; then`,
		`  f=/etc/profile.d/openchat-sandbox-env.sh`,
		`  [ -f "$f" ] || printf '%s\n' '` + sandboxEnvRcMarker + `' 'export PATH="` + entries + `:$PATH"' '` + envLine + `' > "$f" 2>/dev/null || true`,
		`fi`,
		// Tool shells may be non-interactive (`sh -c …`), which sources neither
		// `~/.bashrc` nor `/etc/profile.d`. Linking the toolchain binaries into a
		// system bin dir that is already on the image PATH makes them resolvable
		// regardless of how the shell was started; existing names are never
		// overwritten, so an image-provided shim keeps priority.
		`for d in /usr/local/bin /usr/bin; do`,
		`  [ -w "$d" ] || continue`,
		`  for tool in ` + strings.Join(sandboxToolchainTools, " ") + `; do`,
		`    if [ -x "$d/$tool" ] || ! [ -x "` + SandboxGoBinDir + `/$tool" ]; then continue; fi`,
		`    ln -s "` + SandboxGoBinDir + `/$tool" "$d/$tool" 2>/dev/null || true`,
		`  done`,
		`done`,
	}
}

// SandboxProfileSetupLines renders the lines that run the integration profile
// set-up exactly once per workspace. Provisioning is expensive (private
// checkouts plus the Go workspace) and the resulting workspace is not part of
// the git checkout, so repeating it for every interaction on the same workspace
// is pure latency. The marker is written only on success, so a failed set-up is
// retried instead of being mistaken for a finished one; it lives under `.git/`
// (or `$HOME`) and therefore never appears as an untracked file to the agent.
func SandboxProfileSetupLines(profile string) []string {
	normalized := normalizeDevSetupProfile(profile)
	if normalized == "" {
		normalized = DevSetupProfileDefault
	}
	return []string{
		// The marker path must be assigned before the legacy fallback block,
		// which reads it to decide whether provisioning already happened.
		`if [ -d .git ]; then openchat_sandbox_profile='` + SandboxProfileFile + `'; else openchat_sandbox_profile="$HOME/.openchat-sandbox-profile"; fi`,
		`if [ -x development/scripts/build_tools.sh ] && [ ! -f "$openchat_sandbox_profile" ]; then`,
		`  cli="$(development/scripts/build_tools.sh)" && ` + devSetupPrepareCommand(`"$cli"`, normalized) + ` && printf '%s\n' '` + normalized + `' > "$openchat_sandbox_profile"`,
		`fi`,
	}
}

// devSetupProfileCandidates returns the integration profiles to try, in order,
// for a resolved dev-set-up profile: the requested profile first and
// DevSetupProfileFallback second (deduplicated).
//
// ci#756: the membership of the `full-development` profile ships with the
// *private* integrations manifest, which is materialized from the private `ci`
// repository. A public checkout (and therefore a public CI run) may resolve the
// default profile before that manifest knows it, so provisioning retries the
// last public profile instead of failing the whole sandbox set-up.
func devSetupProfileCandidates(profile string) []string {
	if profile == "" || profile == DevSetupProfileFallback {
		return []string{profile}
	}
	return []string{profile, DevSetupProfileFallback}
}

// devSetupPrepareCommand renders the shell snippet that provisions profile
// through cmd, retrying the fallback profile when the requested one is unknown
// to the integrations manifest.
func devSetupPrepareCommand(cmd, profile string) string {
	parts := make([]string, 0, 2)
	for _, candidate := range devSetupProfileCandidates(profile) {
		if candidate == "" {
			continue
		}
		parts = append(parts, cmd+" prepare --profile "+candidate)
	}
	if len(parts) == 0 {
		return cmd + " prepare"
	}
	command := parts[0]
	for _, part := range parts[1:] {
		command += " || " + part
	}
	return command
}

// sandboxToolchainTools are the binaries this policy can point an agent at.
var sandboxToolchainTools = []string{"go", "gofmt"}

// SandboxToolMissingHint returns the actionable message for a `command not
// found` failure of tool, and whether it is a toolchain binary the sandbox is
// expected to provide.
func SandboxToolMissingHint(tool string) (string, bool) {
	name := strings.TrimSpace(tool)
	for _, known := range sandboxToolchainTools {
		if name != known {
			continue
		}
		return "`" + known + "` is not on PATH. The sandbox image installs the toolchain at " +
			SandboxGoBinDir + "; call it as " + SandboxGoBinDir + "/" + known +
			" or prepend " + strings.Join(SandboxToolchainPathEntries, ":") + " to PATH before giving up on local checks.", true
	}
	return "", false
}

// sandboxShellNotFoundMarkers are the lower-cased shell fragments emitted when a
// command is invoked but not resolvable on PATH.
var sandboxShellNotFoundMarkers = []string{
	": command not found",
	": not found",
	"not found in $path",
	"no such file or directory",
}

// SandboxToolMissingHintFromOutput reports whether a command failed because a
// sandbox toolchain binary was not on PATH, and returns the actionable hint for
// it. It deliberately only claims the toolchain binaries the image installs, so
// a genuinely broken command line is never blamed on the sandbox.
func SandboxToolMissingHintFromOutput(output string) (string, bool) {
	lowered := strings.ToLower(output)
	for _, tool := range sandboxToolchainTools {
		for _, marker := range sandboxShellNotFoundMarkers {
			if strings.Contains(lowered, tool+marker) {
				return SandboxToolMissingHint(tool)
			}
		}
	}
	return "", false
}

// sandboxPrivateModuleMarkers are the lower-cased fragments of a module
// resolution failure that is a *workspace* problem, never a code defect.
var sandboxPrivateModuleMarkers = []string{
	"module lookup disabled",
	"no required module provides package",
	"cannot find module providing package",
	"missing go.sum entry",
	"unrecognized import path",
	"could not read username for",
	"terminal prompts disabled",
	"repository not found",
	"public key authentication failed",
}

// SandboxPrivateModuleHint reports whether a command's output shows the
// integration modules were never materialized, and returns the guidance to
// give the model: configure the workspace, do not "fix" the import.
func SandboxPrivateModuleHint(output string) (string, bool) {
	lowered := strings.ToLower(output)
	for _, marker := range sandboxPrivateModuleMarkers {
		if !strings.Contains(lowered, marker) {
			continue
		}
		return "The integration modules are not materialized in this sandbox (not a source defect). Run `" +
			SandboxProfileSetupCommand + "` once, then re-run the check. The Go workspace resolves " +
			"integrations from the profile checkouts, not from the module proxy; PR CI remains the " +
			"authoritative full-suite gate, so a locally unresolvable private module alone must not " +
			"block the change.", true
	}
	return "", false
}

// sandboxCoderToolPrefix is the tool family whose failures are candidates for
// sandbox-readiness guidance.
const sandboxCoderToolPrefix = "coder_"

// SandboxFailureGuidance returns the readiness guidance to append to a failed
// *coder* tool result, or "" when the failure is an ordinary error the model has
// to fix itself. Wiring this into the tool-error placeholder stops the agent
// from rewriting imports or abandoning a task because its own sandbox was not
// provisioned (ci#739).
func SandboxFailureGuidance(toolName, output string) string {
	if !strings.HasPrefix(strings.TrimSpace(toolName), sandboxCoderToolPrefix) {
		return ""
	}
	if hint, ok := SandboxPrivateModuleHint(output); ok {
		return hint
	}
	if hint, ok := SandboxToolMissingHintFromOutput(output); ok {
		return hint
	}
	return ""
}

// FrameSandboxFailureGuidance appends the guidance of SandboxFailureGuidance to
// a tool-error message, leaving the message untouched when there is nothing
// sandbox-specific to say.
func FrameSandboxFailureGuidance(toolName, toolResult string) string {
	hint := SandboxFailureGuidance(toolName, toolResult)
	if hint == "" {
		return toolResult
	}
	return toolResult + " " + hint
}
