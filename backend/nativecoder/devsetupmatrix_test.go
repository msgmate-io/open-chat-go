package nativecoder

import (
	"strings"
	"testing"
)

// TestDevSetupKnownProfilesMatchIntegrationProfiles pins the public profile
// catalogue against the profiles profile_setup.yaml and DEVELOPMENT.md
// document. A profile outside this list would be passed to
// `openchat-integrations prepare --profile <p>` and fail inside the sandbox, so
// the matrix must know every one of them.
func TestDevSetupKnownProfilesMatchIntegrationProfiles(t *testing.T) {
	for _, profile := range []string{"core-only", "default", "full", "full-ci", "full-android", "full-development"} {
		if !DevSetupKnownProfile(profile) {
			t.Fatalf("documented integration profile %q is missing from DevSetupKnownProfiles", profile)
		}
	}
	for _, profile := range []string{"", "   ", "FULL", "full development", "full-development; rm -rf /", "full-$PROFILE"} {
		if DevSetupKnownProfile(profile) {
			t.Fatalf("profile %q must not be accepted as a known profile", profile)
		}
	}
}

// TestDevSetupRepoPlanForKnownRepos pins the per-repo defaults the ticket asks
// for: the configured ci.msgmate.io development triggers resolve a plan that
// pulls submodules, and the palette variant of the same repository is reported
// next to it.
func TestDevSetupRepoPlanForKnownRepos(t *testing.T) {
	cases := map[string]struct {
		profile string
		variant string
	}{
		"msgmate-io/open-chat-go":               {profile: DevSetupProfileDefault, variant: "coding"},
		"msgmate-io/open-chat-go-ci":            {profile: DevSetupProfileFallback, variant: "coding"},
		"msgmate-io/open-chat-go-helm-chart":    {profile: DevSetupProfileFallback, variant: "k8s"},
		"msgmate-io/open-chat-go-mobile-client": {profile: "full-android", variant: "android"},
	}
	for repo, want := range cases {
		plan := DevSetupRepoPlanFor(repo)
		if !plan.PlanEnabled || plan.PlanSource != DevSetupPlanSourceDefault {
			t.Fatalf("%s: expected the per-repo default plan, got %#v", repo, plan)
		}
		if plan.Profile != want.profile {
			t.Fatalf("%s: profile = %q, want %q", repo, plan.Profile, want.profile)
		}
		if !plan.Submodules {
			t.Fatalf("%s: the development set-up must pull submodules", repo)
		}
		if plan.SandboxVariant != want.variant || !plan.SandboxVariantMapped {
			t.Fatalf("%s: sandbox variant = %q (mapped=%v), want %q", repo, plan.SandboxVariant, plan.SandboxVariantMapped, want.variant)
		}
	}
}

// TestDevSetupRepoPlanForUnknownRepo pins the gap the ticket is about: a trigger
// pointed at a repository outside the per-repo table has no plan and is
// reported as a configuration gap instead of silently starting half-set-up.
func TestDevSetupRepoPlanForUnknownRepo(t *testing.T) {
	plan := DevSetupRepoPlanFor("https://github.com/tbscode/private-playground.git")
	if plan.PlanEnabled || plan.PlanSource != DevSetupPlanSourceUnconfigured {
		t.Fatalf("expected an unconfigured plan, got %#v", plan)
	}
	if plan.SandboxVariant != SandboxDefaultVariant || plan.SandboxVariantMapped {
		t.Fatalf("an unmapped repository must report the default variant, got %q", plan.SandboxVariant)
	}
	gaps := DevSetupMatrixGaps([]DevSetupRepoPlan{plan})
	if len(gaps) != 1 || !strings.Contains(gaps[0], DevSetupConfigKey) {
		t.Fatalf("expected one actionable gap naming %s, got %#v", DevSetupConfigKey, gaps)
	}
}

// TestDevSetupPlanForConfigPrecedence pins the resolution order of an
// interaction configuration: an explicit per-repo declaration wins over the
// per-repo default, a declaration that switches the plan off is reported as
// such, and an absent declaration keeps the default.
func TestDevSetupPlanForConfigPrecedence(t *testing.T) {
	base := map[string]interface{}{
		"coder_sandbox_template": "full-development",
		"coder_dev_setup": map[string]interface{}{
			"profiles": map[string]interface{}{
				"msgmate-io/open-chat-go-ci": "core-only",
			},
		},
	}
	if plan := DevSetupPlanForConfig(base, "msgmate-io/open-chat-go-ci"); plan.PlanSource != DevSetupPlanSourceExplicit || plan.Profile != "core-only" {
		t.Fatalf("expected the explicit per-repo profile, got %#v", plan)
	}
	// A repository outside the table keeps the declared default.
	if plan := DevSetupPlanForConfig(base, "msgmate-io/open-chat-go"); plan.PlanSource != DevSetupPlanSourceExplicit || plan.Profile != DevSetupProfileDefault {
		t.Fatalf("expected the declared default profile, got %#v", plan)
	}
	// No declaration at all keeps the per-repo default.
	if plan := DevSetupPlanForConfig(map[string]interface{}{"model": "gpt"}, "msgmate-io/open-chat-go"); plan.PlanSource != DevSetupPlanSourceDefault {
		t.Fatalf("expected the per-repo default plan, got %#v", plan)
	}
	if plan := DevSetupPlanForConfig(nil, "msgmate-io/open-chat-go"); plan.PlanSource != DevSetupPlanSourceDefault {
		t.Fatalf("a nil config must resolve the per-repo default, got %#v", plan)
	}
	// An explicit "off" is reported instead of being confused with a missing plan.
	off := map[string]interface{}{DevSetupConfigKey: false}
	if plan := DevSetupPlanForConfig(off, "msgmate-io/open-chat-go"); plan.PlanEnabled || plan.PlanSource != DevSetupPlanSourceDisabled {
		t.Fatalf("expected a disabled plan, got %#v", plan)
	}
}

// TestDevSetupMatrixPlanAndValidateKeepsConfiguredReposConsistent is the drift
// guard: every repository the ci.msgmate.io palette maps has a matrix row, the
// rows validate, and the render names the gap of an unmapped repository.
func TestDevSetupMatrixPlanAndValidateKeepsConfiguredReposConsistent(t *testing.T) {
	palette := SandboxPaletteSnapshot()
	repos := make([]string, 0, len(palette.Triggers)+2)
	for _, trigger := range palette.Triggers {
		repos = append(repos, trigger.Repo)
	}
	repos = append(repos, "msgmate-io/open-chat-go", "tbscode/not-a-dev-repo")

	plans := DevSetupMatrixPlan(repos)
	if len(plans) != len(repos)-1 { // one duplicate: open-chat-go is triggered twice
		t.Fatalf("expected %d deduplicated rows, got %d", len(repos)-1, len(plans))
	}
	for i := 1; i < len(plans); i++ {
		if plans[i-1].Repo >= plans[i].Repo {
			t.Fatalf("matrix rows must be sorted and unique, got %q before %q", plans[i-1].Repo, plans[i].Repo)
		}
	}
	if problems := ValidateDevSetupMatrix(plans); len(problems) > 0 {
		t.Fatalf("the resolved matrix must be consistent, got %#v", problems)
	}
	gaps := DevSetupMatrixGaps(plans)
	if len(gaps) != 2 || !strings.Contains(strings.Join(gaps, "\n"), "tbscode/not-a-dev-repo") {
		t.Fatalf("expected the two repositories outside the per-repo table as gaps, got %#v", gaps)
	}
	rendered := RenderDevSetupMatrixMarkdown(plans)
	for _, repo := range []string{"msgmate-io/open-chat-go-ci", "msgmate-io/open-chat-go-helm-chart"} {
		if !strings.Contains(rendered, "`"+repo+"`") {
			t.Fatalf("rendered matrix is missing %q", repo)
		}
	}
	if !strings.Contains(rendered, "## Gaps") || !strings.Contains(rendered, "tbscode/not-a-dev-repo") {
		t.Fatalf("rendered matrix must list the gaps, got:\n%s", rendered)
	}
}

// TestValidateDevSetupMatrixDetectsDefects pins the defect list: an enabled plan
// without a profile or with an unknown profile, an unusable extra repository and
// two extra repositories colliding on one checkout directory.
func TestValidateDevSetupMatrixDetectsDefects(t *testing.T) {
	plans := []DevSetupRepoPlan{
		{Repo: "msgmate-io/open-chat-go", PlanEnabled: true, PlanSource: DevSetupPlanSourceDefault},
		{Repo: "msgmate-io/open-chat-go-ci", PlanEnabled: true, Profile: "full", PlanSource: "guess"},
		{Repo: "msgmate-io/other", PlanEnabled: true, Profile: "not-a-profile", PlanSource: DevSetupPlanSourceDefault},
		{Repo: "", PlanEnabled: true, Profile: "full", PlanSource: DevSetupPlanSourceDefault},
		{Repo: "msgmate-io/extra", PlanEnabled: true, Profile: "full", PlanSource: DevSetupPlanSourceDefault, ExtraRepos: []string{"", "msgmate-io/x/y", "msgmate-io/helm-chart", "msgmate-io/Helm-Chart"}},
		{Repo: "msgmate-io/disabled", PlanEnabled: false, PlanSource: DevSetupPlanSourceDisabled, Profile: "full"},
	}
	joined := strings.Join(ValidateDevSetupMatrix(plans), "\n")
	for _, want := range []string{
		"without a profile",
		"unknown development set-up profile",
		"unknown plan source",
		"matrix entry without a repository",
		"not a usable owner/name slug",
		"collide on the checkout directory",
		"a disabled plan still carries",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected a problem containing %q, got:\n%s", want, joined)
		}
	}
	clean := []DevSetupRepoPlan{{Repo: "msgmate-io/open-chat-go", PlanEnabled: true, Profile: "full", PlanSource: DevSetupPlanSourceDefault}}
	if problems := ValidateDevSetupMatrix(clean); len(problems) != 0 {
		t.Fatalf("a clean plan must validate, got %#v", problems)
	}
}
