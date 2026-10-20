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
	SandboxProfileSetupCommand = ".venv/bin/openchat-integrations prepare --profile full"
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
