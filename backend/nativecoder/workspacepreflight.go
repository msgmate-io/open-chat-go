package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// ci#739: the per-chat coding sandbox the native coder agents work in was
// provisioned "bare", which produced two failures the model cannot fix from the
// inside and which therefore looked like the agent's own mistake:
//
//  1. the Go toolchain the sandbox image installs at /usr/local/go/bin is not on
//     PATH, so a `coder_bash` run of `go test ./backend/...` dies with
//     `sh: go: not found` ("local Go testing is blocked") even though `go` is
//     present and executable at an absolute path;
//  2. the private integration modules the selected profile declares are neither
//     materialized (no `openchat-integrations setup`) nor resolvable (GOPRIVATE
//     is unset, so the public proxy answers 404 for the private namespace), so
//     the first build of any package that imports them fails with a
//     module-resolution error that reads like a source defect.
//
// Both are *workspace provisioning*, so the agent burned its failed-tool budget
// on them and abandoned otherwise fine runs. This file owns the public half of
// the fix: a deterministic bootstrap script the sandbox runs before the agent's
// first build/test, plus a classifier that turns a raw shell failure into a
// reason the model can act on instead of retrying blindly.
//
// Nothing here ever carries a credential *value*: the rendered script only ever
// names the credential environment variable the sandbox already holds, so the
// script (and any log or tool result that shows it) stays safe to print.

const (
	// WorkspaceGoBinDir is where the sandbox image installs the Go toolchain.
	WorkspaceGoBinDir = "/usr/local/go/bin"
	// WorkspaceGoPrivateNamespace are the module paths that must be resolved
	// straight from git instead of the public module proxy.
	WorkspaceGoPrivateNamespace = "github.com/msgmate-io/*"
	// WorkspacePreflightMarker prefixes every status line the rendered script
	// prints, so a later `coder_bash` batch can be classified without scraping
	// unrelated build output.
	WorkspacePreflightMarker = "workspace_preflight"
	// WorkspaceDefaultIntegrationProfile is the profile a coder sandbox starts
	// from: it needs no private module at all, so a targeted `go test` of the
	// public packages works even when the private checkouts are unavailable.
	WorkspaceDefaultIntegrationProfile = "core-only"
)

// WorkspaceGoEnvVars is the module-resolution environment a coder sandbox needs
// for a checkout that references the private namespace: the private prefix is
// never sent to the public proxy/checksum database, `-mod=mod` lets a checkout
// without a complete go.sum still compile, and GOTOOLCHAIN=auto keeps a go.mod
// that names a newer toolchain from silently failing on a plain download error.
func WorkspaceGoEnvVars() map[string]string {
	return map[string]string{
		"GOPRIVATE":   WorkspaceGoPrivateNamespace,
		"GOFLAGS":     "-mod=mod",
		"GOPROXY":     "https://proxy.golang.org,direct",
		"GOTOOLCHAIN": "auto",
	}
}

// WorkspaceReadinessReason is the classified outcome of a sandbox workspace
// build/test command.
type WorkspaceReadinessReason string

const (
	// WorkspaceReady means the command produced no provisioning failure.
	WorkspaceReady WorkspaceReadinessReason = "ready"
	// WorkspaceToolchainMissing is the "go is installed but not on PATH" case.
	WorkspaceToolchainMissing WorkspaceReadinessReason = "toolchain_missing"
	// WorkspaceIntegrationModulesMissing is the "profile modules were never
	// materialized" case.
	WorkspaceIntegrationModulesMissing WorkspaceReadinessReason = "integration_modules_missing"
	// WorkspacePrivateModuleUnresolved is the "private namespace needs git +
	// GOPRIVATE instead of the public proxy" case.
	WorkspacePrivateModuleUnresolved WorkspaceReadinessReason = "private_module_unresolved"
	// WorkspaceUnknownFailure is any other failure, ie a real build/test error
	// that the model owns.
	WorkspaceUnknownFailure WorkspaceReadinessReason = "unknown"
)

// workspaceReasonMarkers maps the lower-cased fragments of a provisioning
// failure onto its reason. The order is the classification order: the toolchain
// (which explains a whole batch of "not found" lines) wins over the module
// reasons, and the integration-modules reason wins over the generic private
// module one, because `openchat-integrations setup` is the fix that covers both.
var workspaceReasonMarkers = []struct {
	reason  WorkspaceReadinessReason
	markers []string
}{
	{
		reason: WorkspaceToolchainMissing,
		markers: []string{
			"go: not found",
			"go: command not found",
			"command not found: go",
			"exec: \"go\": executable file not found",
			"go: cannot find the go toolchain",
		},
	},
	{
		reason: WorkspaceIntegrationModulesMissing,
		markers: []string{
			"openchat-integrations: not found",
			"openchat-integrations: command not found",
			".integrations/private",
			"clients/integrations: no such file",
			"integration modules are not materialized",
		},
	},
	{
		reason: WorkspacePrivateModuleUnresolved,
		markers: []string{
			"module lookup disabled",
			"could not read username for 'https://github.com'",
			"terminal prompts disabled",
			"no required module provides package github.com/msgmate-io",
			"is not in std",
			"unrecognized import path",
			"reading https://proxy.golang.org",
			"403 forbidden",
		},
	},
}

// ClassifyWorkspaceReadiness turns the captured output of a sandbox build/test
// command into a provisioning reason. Empty output is "ready"; anything
// unrecognised stays WorkspaceUnknownFailure, so a genuine compile error is
// never excused as infrastructure.
func ClassifyWorkspaceReadiness(output string) WorkspaceReadinessReason {
	lower := strings.ToLower(output)
	if strings.TrimSpace(lower) == "" {
		return WorkspaceReady
	}
	for _, entry := range workspaceReasonMarkers {
		for _, marker := range entry.markers {
			if strings.Contains(lower, marker) {
				return entry.reason
			}
		}
	}
	return WorkspaceUnknownFailure
}

// IsWorkspaceProvisioningFailure reports whether a reason describes the sandbox
// rather than the agent's own change. Such a failure must not consume the
// failed-tool budget (ci#688/ci#739) and must not be reported as a code defect.
func IsWorkspaceProvisioningFailure(reason WorkspaceReadinessReason) bool {
	switch reason {
	case WorkspaceToolchainMissing, WorkspaceIntegrationModulesMissing, WorkspacePrivateModuleUnresolved:
		return true
	default:
		return false
	}
}

// WorkspaceReadinessHint is the model-facing next step for a provisioning
// reason, phrased so the agent repairs the workspace instead of retrying the
// same command or abandoning the task. A reason the model owns returns "".
func WorkspaceReadinessHint(reason WorkspaceReadinessReason) string {
	switch reason {
	case WorkspaceToolchainMissing:
		return fmt.Sprintf(
			"The Go toolchain is installed in this sandbox but is not on PATH (ci#739): "+
				"export PATH=\"%s:$PATH\" (the rendered workspace preflight script does this) and re-run the command, "+
				"instead of reporting that Go is unavailable or abandoning the build.",
			WorkspaceGoBinDir,
		)
	case WorkspaceIntegrationModulesMissing:
		return "The selected profile's private integration modules are not materialized in this sandbox checkout (ci#739): " +
			"run `.venv/bin/openchat-integrations setup --profile " + WorkspaceDefaultIntegrationProfile + "` " +
			"(or `--profile full` for the fully-fledged checkout) before building a package that imports them; " +
			"the missing package is a provisioning gap, not a defect in the code you edited."
	case WorkspacePrivateModuleUnresolved:
		return "The build could not resolve a module from the private namespace " + WorkspaceGoPrivateNamespace + " (ci#739): " +
			"run the workspace preflight (it exports GOPRIVATE and installs a git credential helper that reads the sandbox's " +
			"GitHub token from the environment without storing it), or build from a checkout whose modules are already " +
			"initialized. Do not treat this as a compile error in the code you changed."
	default:
		return ""
	}
}

// RenderWorkspacePreflightScript renders the idempotent bootstrap the sandbox
// runs before the agent's first build/test: put the toolchain on PATH, export
// the module-resolution environment, name the credential helper, materialize the
// profile's integration modules, and print one machine-readable status line.
//
// The script is deliberately non-fatal (`|| true`, no `set -e`) so a partial
// sandbox still lets the agent work, and it never prints or stores a token value.
func RenderWorkspacePreflightScript() string {
	env := WorkspaceGoEnvVars()
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# ci#739 workspace preflight: run this before the first build/test in a coder sandbox.\n")
	b.WriteString("set -u\n")
	fmt.Fprintf(
		&b,
		"if [ -d %s ] && ! command -v go >/dev/null 2>&1; then\n  PATH=\"%s:$PATH\"; export PATH\nfi\n",
		WorkspaceGoBinDir,
		WorkspaceGoBinDir,
	)
	b.WriteString("if command -v go >/dev/null 2>&1; then\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "  go env -w %s='%s' >/dev/null 2>&1 || true\n", key, env[key])
	}
	b.WriteString("  if [ -n \"${GITHUB_TOKEN:-${GH_TOKEN:-}}\" ]; then\n")
	b.WriteString("    git config --global credential.helper '!f() { echo username=x-access-token; echo password=${GITHUB_TOKEN:-${GH_TOKEN}}; }; f' >/dev/null 2>&1 || true\n")
	b.WriteString("  fi\n")
	b.WriteString("fi\n")
	b.WriteString("if command -v openchat-integrations >/dev/null 2>&1; then\n")
	fmt.Fprintf(
		&b,
		"  openchat-integrations setup --profile %s >/dev/null 2>&1 || true\n",
		WorkspaceDefaultIntegrationProfile,
	)
	b.WriteString("fi\n")
	b.WriteString("if command -v go >/dev/null 2>&1; then\n")
	fmt.Fprintf(
		&b,
		"  printf '%s status=ready toolchain=%%s\\n' \"$(go version 2>&1 | tr -d '\\n')\"\n",
		WorkspacePreflightMarker,
	)
	b.WriteString("else\n")
	fmt.Fprintf(
		&b,
		"  printf '%s status=toolchain_missing hint=\"add %s to PATH\"\\n'\n",
		WorkspacePreflightMarker,
		WorkspaceGoBinDir,
	)
	b.WriteString("fi\n")
	return b.String()
}
