package nativecoder

import (
	"strings"
	"testing"
)

func TestRenderWorkspacePreflightScriptPutsToolchainOnPath(t *testing.T) {
	script := RenderWorkspacePreflightScript()

	if !strings.Contains(script, `PATH="`+WorkspaceGoBinDir+`:$PATH"`) {
		t.Fatalf("preflight must prepend %s to PATH, got:\n%s", WorkspaceGoBinDir, script)
	}
	// The toolchain is not on PATH to begin with, so the script must add it
	// conditionally instead of duplicating it on every run.
	if !strings.Contains(script, "! command -v go") {
		t.Fatalf("preflight must not assume PATH already contains the toolchain:\n%s", script)
	}
	// Non-fatal: a partial sandbox must still let the agent work.
	if strings.Contains(script, "set -e") {
		t.Fatalf("preflight must stay non-fatal, got:\n%s", script)
	}
}

func TestRenderWorkspacePreflightScriptConfiguresPrivateModules(t *testing.T) {
	script := RenderWorkspacePreflightScript()

	for _, key := range []string{"GOPRIVATE", "GOPROXY", "GOFLAGS"} {
		if !strings.Contains(script, "go env -w "+key+"=") {
			t.Fatalf("preflight must set %s via `go env -w`, got:\n%s", key, script)
		}
	}
	if !strings.Contains(script, WorkspaceGoPrivateNamespace) {
		t.Fatalf("preflight must scope GOPRIVATE to %s:\n%s", WorkspaceGoPrivateNamespace, script)
	}
	if !strings.Contains(script, "openchat-integrations setup --profile "+WorkspaceDefaultIntegrationProfile) {
		t.Fatalf("preflight must materialize the profile's integration modules:\n%s", script)
	}
	// One machine-readable status line so a later batch can be classified.
	if !strings.Contains(script, WorkspacePreflightMarker+" status=") {
		t.Fatalf("preflight must report one status line, got:\n%s", script)
	}
}

// The rendered script ends up in tool results and logs, so it must never embed a
// credential value - only the name of the environment variable that holds it.
func TestRenderWorkspacePreflightScriptNeverEmbedsCredentialValues(t *testing.T) {
	script := RenderWorkspacePreflightScript()

	for _, forbidden := range []string{"GITHUB_TOKEN=", "GH_TOKEN=", "x-access-token:"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("preflight must not embed a credential (%q):\n%s", forbidden, script)
		}
	}
	if !strings.Contains(script, "${GITHUB_TOKEN:-${GH_TOKEN}}") {
		t.Fatalf("preflight must reference the credential environment variable, got:\n%s", script)
	}
	if !strings.Contains(script, "credential.helper") {
		t.Fatalf("preflight must install the credential helper:\n%s", script)
	}
}

func TestClassifyWorkspaceReadiness(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   WorkspaceReadinessReason
	}{
		{name: "empty output is ready", output: "", want: WorkspaceReady},
		{name: "whitespace only is ready", output: "  \n\t", want: WorkspaceReady},
		{
			name:   "go missing from PATH",
			output: "sh: go: not found\nmake: *** [test] Error 127",
			want:   WorkspaceToolchainMissing,
		},
		{
			name:   "absolute toolchain reported as unavailable",
			output: "exec: \"go\": executable file not found in $PATH",
			want:   WorkspaceToolchainMissing,
		},
		{
			name:   "integration modules not materialized",
			output: "openchat-integrations: not found\nls: cannot access '.integrations/private': No such file or directory",
			want:   WorkspaceIntegrationModulesMissing,
		},
		{
			name:   "private namespace not resolvable through the public proxy",
			output: "go: github.com/msgmate-io/openchat-integrations@v0.4.1: reading https://proxy.golang.org/...: 404 Not Found",
			want:   WorkspacePrivateModuleUnresolved,
		},
		{
			name:   "anonymous git prompts disabled",
			output: "fatal: could not read Username for 'https://github.com': terminal prompts disabled",
			want:   WorkspacePrivateModuleUnresolved,
		},
		{
			name:   "a real compile error stays the model's",
			output: "./foo.go:12:2: undefined: bar",
			want:   WorkspaceUnknownFailure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyWorkspaceReadiness(tc.output); got != tc.want {
				t.Fatalf("ClassifyWorkspaceReadiness(%q) = %q, want %q", tc.output, got, tc.want)
			}
		})
	}
}

func TestIsWorkspaceProvisioningFailure(t *testing.T) {
	for _, reason := range []WorkspaceReadinessReason{
		WorkspaceToolchainMissing,
		WorkspaceIntegrationModulesMissing,
		WorkspacePrivateModuleUnresolved,
	} {
		if !IsWorkspaceProvisioningFailure(reason) {
			t.Fatalf("%q must be a provisioning failure", reason)
		}
		if WorkspaceReadinessHint(reason) == "" {
			t.Fatalf("%q must carry a model-facing hint", reason)
		}
	}
	for _, reason := range []WorkspaceReadinessReason{WorkspaceReady, WorkspaceUnknownFailure} {
		if IsWorkspaceProvisioningFailure(reason) {
			t.Fatalf("%q must not be excused as provisioning", reason)
		}
		if hint := WorkspaceReadinessHint(reason); hint != "" {
			t.Fatalf("%q must not claim a provisioning hint, got %q", reason, hint)
		}
	}
}

func TestWorkspaceReadinessHintNamesTheNextStep(t *testing.T) {
	cases := map[WorkspaceReadinessReason]string{
		WorkspaceToolchainMissing:          WorkspaceGoBinDir,
		WorkspaceIntegrationModulesMissing: "setup --profile",
		WorkspacePrivateModuleUnresolved:   "GOPRIVATE",
	}
	for reason, want := range cases {
		hint := WorkspaceReadinessHint(reason)
		if !strings.Contains(hint, want) {
			t.Fatalf("hint for %q must mention %q, got %q", reason, want, hint)
		}
		if !strings.Contains(hint, "ci#739") {
			t.Fatalf("hint for %q must cite the ticket, got %q", reason, hint)
		}
	}
}
