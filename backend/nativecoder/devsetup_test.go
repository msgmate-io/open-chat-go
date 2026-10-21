package nativecoder

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDevSetupRoundTrip pins the ci#756 contract: a native coder interaction
// that knows its repository gets a development set-up script materialized into
// `coder_sandbox_setup_commands`, an interaction without a repository does not,
// and an explicitly configured script always wins.
func TestDevSetupRoundTrip(t *testing.T) {
	if got := DevSetupRepoForInteraction(nil, nil, nil, nil); got != "" {
		t.Fatalf("no inputs must not resolve a repository, got %q", got)
	}
	if spec := DevSetupSpecForRepo("someone/unknown-repo"); spec.Enabled {
		t.Fatal("an unknown repository must not get a built-in plan")
	}

	spec := DevSetupSpecForRepo("msgmate-io/open-chat-go")
	if !spec.Enabled || spec.Profile != DevSetupProfileDefault || !spec.Submodules {
		t.Fatalf("known development repo must resolve the full default profile, got %#v", spec)
	}
	if DevSetupScript(spec) == "" {
		t.Fatal("expected a non-empty development set-up script")
	}

	cfg := map[string]interface{}{"coder_sandbox_template": "full-development"}
	out := WithDevSetup(cfg, "msgmate-io/open-chat-go", "")
	if _, mutated := cfg[SandboxSetupCommandsConfigKey]; mutated {
		t.Fatal("WithDevSetup must not mutate its input config")
	}
	script, ok := out[SandboxSetupCommandsConfigKey].(string)
	if !ok || script == "" {
		t.Fatalf("expected a materialized set-up script, got %#v", out[SandboxSetupCommandsConfigKey])
	}

	// A non-coder agent is never touched.
	if got := WithDevSetup(map[string]interface{}{"model": "gpt"}, "msgmate-io/open-chat-go", ""); got[SandboxSetupCommandsConfigKey] != nil {
		t.Fatal("non-coder configs must stay unchanged")
	}

	// An explicitly configured script must be preserved verbatim.
	explicit := map[string]interface{}{
		"coder_sandbox_template":      "full-development",
		SandboxSetupCommandsConfigKey: "echo custom",
	}
	if got := WithDevSetup(explicit, "msgmate-io/open-chat-go", ""); got[SandboxSetupCommandsConfigKey] != "echo custom" {
		t.Fatalf("explicit script must win, got %#v", got[SandboxSetupCommandsConfigKey])
	}

	// The environment kill-switch rolls the per-repo default back.
	if got := WithDevSetup(cfg, "msgmate-io/open-chat-go", "0"); got[SandboxSetupCommandsConfigKey] != nil {
		t.Fatal("kill-switch must disable the automatic set-up")
	}

	// An explicit per-interaction declaration still applies while the
	// deployment-wide default is off.
	if got := WithDevSetup(
		map[string]interface{}{"coder_sandbox_template": "full-development", DevSetupConfigKey: "full-android"},
		"",
		"0",
	); got[SandboxSetupCommandsConfigKey] == nil {
		t.Fatal("explicit per-interaction plan must survive the kill-switch")
	}
}

// TestDevSetupRepoForInteractionNormalizes checks that the interaction repo
// (explicit override wins over the effective config) is normalized to owner/name.
func TestDevSetupRepoForInteractionNormalizes(t *testing.T) {
	overrides := map[string]interface{}{"git_repo": "https://github.com/msgmate-io/open-chat-go.git"}
	if got := DevSetupRepoForInteraction(nil, nil, nil, overrides); got != "msgmate-io/open-chat-go" {
		t.Fatalf("expected normalized repository, got %q", got)
	}
	if got := DevSetupRepoForInteraction(nil, []string{"repo:msgmate-io/open-chat-go-ci"}, nil, nil); got != "msgmate-io/open-chat-go-ci" {
		t.Fatalf("expected repository from tag, got %q", got)
	}
}

// TestDevSetupScriptProvisionsSubmoduleFree pins the ci#739 fix: the generated
// script must bootstrap the integration CLI through `build_tools.sh` (which
// installs the CLI from upstream when the build-tools submodule is absent)
// instead of gating provisioning on the submodule directory, and it must persist
// the toolchain environment for the agent's later shells.
func TestDevSetupScriptProvisionsSubmoduleFree(t *testing.T) {
	script := DevSetupScript(DevSetupSpecForRepo("msgmate-io/open-chat-go"))
	for _, want := range []string{
		"development/scripts/build_tools.sh",
		"prepare --profile full-ci",
		"check_coder_sandbox_readiness.sh",
		"export PATH=\"/usr/local/go/bin",
		"export GOPRIVATE=",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script must contain %q, got %q", want, script)
		}
	}
	if strings.Contains(script, "if [ -d development/build-tools ] && command -v python3") {
		t.Fatal("provisioning must not be gated on the build-tools submodule directory")
	}
	// A re-run must not pay the full provisioning cost twice.
	if !strings.Contains(script, "openchat_sandbox_profile") {
		t.Fatal("script must mark a successful profile set-up")
	}
}

// TestDevSetupScriptRetriesPublicFallbackProfile pins the ci#756 contract: the
// default profile is the batteries-included `full-development` one, but the
// generated script must still degrade to the last public profile when the
// private integrations manifest does not know it yet (otherwise every coding
// sandbox fails to start on an unknown profile).
func TestDevSetupScriptRetriesPublicFallbackProfile(t *testing.T) {
	spec := DevSetupSpecForRepo("msgmate-io/open-chat-go")
	if spec.Profile != DevSetupProfileDefault {
		t.Fatalf("default profile = %q, want %q", spec.Profile, DevSetupProfileDefault)
	}
	script := DevSetupScript(spec)
	first := "prepare --profile " + DevSetupProfileDefault
	second := "prepare --profile " + DevSetupProfileFallback
	if !strings.Contains(script, first+" || ") {
		t.Fatalf("script must retry the fallback profile, got %q", script)
	}
	if strings.Index(script, first) > strings.Index(script, second) {
		t.Fatalf("script must try the requested profile first, got %q", script)
	}
	// The fallback itself must not be retried again (no unbounded chain).
	if strings.Contains(script, second+" || "+second) {
		t.Fatalf("fallback profile must not be retried twice, got %q", script)
	}
}

// TestDevSetupPrepareCommand pins the rendering of the provisioning snippet for
// the explicit per-interaction profiles.
func TestDevSetupPrepareCommand(t *testing.T) {
	cases := []struct{ profile, want string }{
		{"", ".venv/bin/openchat-integrations prepare"},
		{DevSetupProfileFallback, ".venv/bin/openchat-integrations prepare --profile full-ci"},
		{
			"full-development",
			".venv/bin/openchat-integrations prepare --profile full-development || " +
				".venv/bin/openchat-integrations prepare --profile full-ci",
		},
	}
	for _, tc := range cases {
		got := devSetupPrepareCommand(".venv/bin/openchat-integrations", tc.profile)
		if got != tc.want {
			t.Fatalf("profile %q: got %q, want %q", tc.profile, got, tc.want)
		}
	}
}

// TestDevSetupScriptWarnsWhenUnprovisionable checks that a checkout with
// neither the script nor the submodule reports the gap instead of silently
// handing an unprepared workspace to the model.
func TestDevSetupScriptWarnsWhenUnprovisionable(t *testing.T) {
	script := DevSetupScript(DevSetupSpec{Enabled: true, Profile: DevSetupProfileDefault})
	if !strings.Contains(script, "cannot be provisioned") {
		t.Fatalf("expected an unprepared-workspace warning, got %q", script)
	}
	if !strings.HasPrefix(script, "set -e") {
		t.Fatal("script must remain fail-fast")
	}
}

// TestDevSetupScriptIsValidShell parses every generated variant with the shell
// it will actually run under. The set-up script is assembled from many string
// fragments (quoting, `for` loops, `$(...)`), so a typo would only surface as an
// opaque sandbox bootstrap failure — this keeps it a local test failure.
func TestDevSetupScriptIsValidShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell available to parse the script")
	}
	cases := map[string]DevSetupSpec{
		"default":       DevSetupSpecForRepo("msgmate-io/open-chat-go"),
		"no-submodules": {Enabled: true, Profile: DevSetupProfileDefault},
		"custom": {
			Enabled:  true,
			Profile:  "full-ci",
			Commands: []string{"echo warm-cache"},
			ExtraRepos: []string{
				"msgmate-io/open-chat-go-build-tools",
			},
		},
	}
	for name, spec := range cases {
		script := DevSetupScript(spec)
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: generated script is not valid shell: %v\n%s\nscript:\n%s", name, err, out, script)
		}
	}
}

// TestResolveDevSetupExplicit pins the configurable per-interaction shape.
func TestResolveDevSetupExplicit(t *testing.T) {
	spec, ok := ResolveDevSetup(map[string]interface{}{DevSetupConfigKey: "full-android"})
	if !ok || spec.Profile != "full-android" || !spec.Submodules {
		t.Fatalf("string profile must resolve, got %#v ok=%v", spec, ok)
	}

	spec, ok = ResolveDevSetup(map[string]interface{}{
		DevSetupConfigKey: map[string]interface{}{
			"profile":    "full-ci",
			"submodules": false,
			"repos":      []interface{}{"msgmate-io/open-chat-go-build-tools"},
			"commands":   "echo warm-cache",
		},
	})
	if !ok || spec.Profile != "full-ci" || spec.Submodules {
		t.Fatalf("map form must resolve, got %#v ok=%v", spec, ok)
	}
	if len(spec.ExtraRepos) != 1 || len(spec.Commands) != 1 {
		t.Fatalf("map form must carry repos/commands, got %#v", spec)
	}
	script := DevSetupScript(spec)
	if !strings.Contains(script, "openchat-integrations prepare --profile full-ci") || !strings.Contains(script, "echo warm-cache") {
		t.Fatalf("script must run the profile and the extra command, got %q", script)
	}

	if _, ok := ResolveDevSetup(map[string]interface{}{}); ok {
		t.Fatal("missing key must not resolve a plan")
	}
	if _, ok := ResolveDevSetup(map[string]interface{}{DevSetupConfigKey: false}); ok {
		t.Fatal("explicit false must disable the plan")
	}
	if _, ok := ResolveDevSetup(map[string]interface{}{DevSetupConfigKey: "rm -rf /"}); ok {
		t.Fatal("values outside the profile charset must be rejected")
	}
}
