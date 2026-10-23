package nativecoder

import (
	"strings"
	"testing"
)

// TestResolveDevSetupPerRepoProfiles pins the per-repository profile table of an
// explicit `coder_dev_setup` block (ci#756): one bot/interaction configuration
// can declare the development set-up each repository it serves needs, and the
// profile of the dispatched repository wins.
func TestResolveDevSetupPerRepoProfiles(t *testing.T) {
	cfg := map[string]interface{}{
		"coder_dev_setup": map[string]interface{}{
			"profiles": map[string]interface{}{
				"msgmate-io/open-chat-go-ci":     "full-ci",
				"msgmate-io/open-chat-go":        "full-development",
				"msgmate-io/open-chat-go-mobile": "full-android",
			},
		},
	}
	spec, explicit := ResolveDevSetup(cfg)
	if !explicit {
		t.Fatal("a profiles-only coder_dev_setup block must resolve a plan")
	}
	if len(spec.Profiles) != 3 {
		t.Fatalf("expected 3 per-repo profiles, got %#v", spec.Profiles)
	}
	if got := spec.Profile; got != DevSetupProfileDefault {
		t.Fatalf("expected the default profile to stay the plan default, got %q", got)
	}
}

// TestDevSetupSpecForRepoPrefersPerRepoProfile pins the resolution order: the
// per-repo table of an explicit plan beats its default profile, the longest
// matching prefix wins, and a repository outside the table keeps the default.
func TestDevSetupSpecForRepoPrefersPerRepoProfile(t *testing.T) {
	spec := DevSetupSpec{
		Enabled:    true,
		Profile:    "full-development",
		Submodules: true,
		Profiles: []DevSetupRepoProfile{
			{RepoPrefix: "msgmate-io/open-chat-go-ci", Profile: "full-ci"},
			{RepoPrefix: "msgmate-io/open-chat-go-mobile", Profile: "full-android"},
			{RepoPrefix: "msgmate-io/open-chat-go", Profile: "core-only"},
		},
	}
	cases := map[string]string{
		"msgmate-io/open-chat-go-ci":                            "full-ci",
		"https://github.com/msgmate-io/open-chat-go-mobile.git": "full-android",
		"msgmate-io/open-chat-go":                               "core-only",
		"msgmate-io/open-chat-go-build-tools":                   "core-only",
		"msgmate-io/other-repo":                                 "full-development",
		"":                                                      "full-development",
	}
	for repo, want := range cases {
		if got := spec.forRepo(repo).Profile; got != want {
			t.Fatalf("forRepo(%q) = %q, want %q", repo, got, want)
		}
	}
}

// TestWithDevSetupAppliesPerRepoProfile pins the end-to-end observable effect:
// the generated sandbox script provisions the profile of the dispatched
// repository, never the profile of a sibling repository.
func TestWithDevSetupAppliesPerRepoProfile(t *testing.T) {
	base := map[string]interface{}{
		"coder_sandbox_template": "full-development",
		"coder_dev_setup": map[string]interface{}{
			"profiles": map[string]interface{}{
				"msgmate-io/open-chat-go-ci": "core-only",
				"msgmate-io/open-chat-go":    "full-development",
			},
		},
	}
	for repo, want := range map[string]string{
		"msgmate-io/open-chat-go-ci": "core-only",
		"msgmate-io/open-chat-go":    "full-development",
	} {
		out := WithDevSetup(base, repo, "")
		script, ok := out[SandboxSetupCommandsConfigKey].(string)
		if !ok || script == "" {
			t.Fatalf("expected a generated set-up script for %q, got %#v", repo, out[SandboxSetupCommandsConfigKey])
		}
		if !strings.Contains(script, "openchat_sandbox_profile") {
			t.Fatalf("the generated script for %q must persist the profile marker", repo)
		}
		if !strings.Contains(script, want) {
			t.Fatalf("the generated script for %q must request the %q profile", repo, want)
		}
	}
}
