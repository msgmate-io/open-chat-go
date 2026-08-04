package nativecoder

import (
	"strings"
	"testing"
)

func TestCoderDevSetupFleetCoversNativeRuntimes(t *testing.T) {
	seen := map[string]bool{}
	for _, entry := range CoderDevSetupFleet {
		if entry.Bot == "" || entry.UseCase == "" {
			t.Fatalf("fleet entry must name bot and use-case: %+v", entry)
		}
		if seen[entry.Bot] {
			t.Fatalf("duplicate fleet entry for %s", entry.Bot)
		}
		seen[entry.Bot] = true
	}
	for _, runtime := range []string{NativeBotPlanRuntime, NativeBotReplRuntime, NativeBotReviewRuntime, NativeBotLeadRuntime} {
		if !seen[runtime] {
			t.Fatalf("native coder runtime %s is missing from CoderDevSetupFleet", runtime)
		}
	}
}

func TestCoderDevSetupFleetHasNoCoverageGaps(t *testing.T) {
	plans := DevSetupFleetPlans(CoderDevSetupFleet)
	if len(plans) != len(CoderDevSetupFleet) {
		t.Fatalf("expected %d plans, got %d", len(CoderDevSetupFleet), len(plans))
	}
	for _, plan := range plans {
		if plan.Status != DevSetupFleetStatusSatisfied {
			t.Fatalf("bot %s is %s: %v", plan.Bot, plan.Status, plan.Gaps)
		}
		if len(plan.Repos) == 0 {
			t.Fatalf("bot %s resolved no repository plan", plan.Bot)
		}
		for _, repo := range plan.Repos {
			if !DevSetupKnownProfile(repo.Profile) {
				t.Fatalf("bot %s repo %s uses unknown profile %q", plan.Bot, repo.Repo, repo.Profile)
			}
			if !repo.Submodules {
				t.Fatalf("bot %s repo %s does not pull submodules", plan.Bot, repo.Repo)
			}
		}
	}
	if gaps := DevSetupFleetGaps(plans); len(gaps) != 0 {
		t.Fatalf("unexpected gaps: %v", gaps)
	}
}

func TestDevSetupFleetAuditFlagsUnconfiguredRepo(t *testing.T) {
	plans := DevSetupFleetPlans([]DevSetupFleetEntry{{
		Bot:     "msgmate-coder-repl-bot",
		UseCase: "repl",
		Repos:   []string{"example/unknown-repo"},
	}})
	if len(plans) != 1 || plans[0].Status != DevSetupFleetStatusUnconfigured {
		t.Fatalf("expected unconfigured status, got %+v", plans)
	}
	if gaps := DevSetupFleetGaps(plans); len(gaps) == 0 {
		t.Fatal("expected a gap for the unconfigured repository")
	}
}

func TestRenderDevSetupFleetMarkdownListsBotsAndGaps(t *testing.T) {
	rendered := RenderDevSetupFleetMarkdown(DevSetupFleetPlans(CoderDevSetupFleet))
	for _, want := range []string{
		"schema v",
		"| Bot |",
		NativeBotPlanRuntime,
		"msgmate-io/open-chat-go",
		DevSetupProfileDefault,
		"Coverage gaps: 0",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered fleet report lacks %q:\n%s", want, rendered)
		}
	}
}

func TestRenderDevSetupConfigFragmentIsPerRepo(t *testing.T) {
	fragment := RenderDevSetupConfigFragment()
	if !strings.HasPrefix(fragment, "#") {
		t.Fatalf("fragment should start with the non-secret header:\n%s", fragment)
	}
	for _, want := range []string{
		DevSetupConfigKey + ":",
		"profile: " + DevSetupProfileDefault,
		"submodules: true",
		"msgmate-io/open-chat-go: " + DevSetupProfileDefault,
		"msgmate-io/open-chat-go-ci: full-ci",
		"msgmate-io/open-chat-go-mobile: full-android",
		"msgmate-io/open-chat-go-build-tools: core-only",
	} {
		if !strings.Contains(fragment, want) {
			t.Fatalf("fragment lacks %q:\n%s", want, fragment)
		}
	}
	// A declared profile must always be known, so the fragment can never reach a
	// sandbox with an unusable `prepare --profile` argument.
	for _, entry := range DefaultDevSetupProfiles {
		if !DevSetupKnownProfile(entry.Profile) {
			t.Fatalf("default profile for %s is not in the catalogue: %q", entry.RepoPrefix, entry.Profile)
		}
	}
}
