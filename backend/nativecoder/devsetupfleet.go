package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the per-bot / per-use-case audit surface of the ci#756
// development set-up. devsetup.go owns the plan a single interaction resolves,
// devsetupmatrix.go audits configured repositories; this file audits the *coder
// fleet*: which bot runs which use-case against which repository and which
// development set-up it therefore needs.
//
// Why it exists: the per-repo default (DefaultDevSetupProfiles) reaches every
// interaction automatically, so the deployment does not have to list a bot for
// the workspace to be prepared. The ci.msgmate.io config delta however only
// pinned the set-up for the runtime-selector bot, so an operator had no way to
// see — before opening a chat — whether the remaining coder bots are covered by
// the default or silently run against a repository outside the table (bare
// checkout: no submodules, no Helm/ci/mobile tooling, model installs ad hoc).
//
//	plans := nativecoder.DevSetupFleetPlans(nativecoder.CoderDevSetupFleet)
//	gaps := nativecoder.DevSetupFleetGaps(plans)          // coverage defects
//	nativecoder.RenderDevSetupFleetMarkdown(plans)        // operator table
//	nativecoder.RenderDevSetupConfigFragment()            // ci delta fragment
//
// `go run ./backend/scripts/dev_set_setup_report --fleet` renders the same data.
// Like the sibling registries this is data-only: bot names, repository slugs and
// profile names, never a token, kubeconfig, user path or credential.
const (
	// DevSetupFleetVersion is the schema version of the fleet report.
	DevSetupFleetVersion = 1

	// DevSetupFleetStatusSatisfied marks a bot every declared repository has a
	// known-profile plan for (explicit or per-repo default).
	DevSetupFleetStatusSatisfied = "satisfied"
	// DevSetupFleetStatusUnconfigured marks a bot whose repository no plan
	// covers: it would start half-set-up.
	DevSetupFleetStatusUnconfigured = "unconfigured"
	// DevSetupFleetStatusDisabled marks a bot whose configuration switched the
	// set-up off.
	DevSetupFleetStatusDisabled = "disabled"

	// DevSetupFleetDefaultRepo is the repository a fleet entry runs against
	// when it declares none.
	DevSetupFleetDefaultRepo = "msgmate-io/open-chat-go"
)

// DevSetupFleetEntry is one coder bot and the repositories its use-case is
// dispatched against.
type DevSetupFleetEntry struct {
	// Bot is the ci.msgmate.io bot name.
	Bot string
	// UseCase is the coder stage the bot serves (plan, repl, review, …).
	UseCase string
	// Repos are the repositories the use-case is dispatched against. Empty
	// means DevSetupFleetDefaultRepo.
	Repos []string
	// Config is the interaction's shared config, when the bot pins the set-up
	// explicitly instead of relying on the per-repo default.
	Config map[string]interface{}
}

// CoderDevSetupFleet is the catalogue of native coder bots and the repositories
// their use-case serves. It is the fleet counterpart of DefaultDevSetupProfiles
// and is kept from drifting apart from the native runtime names by
// devsetupfleet_test.go.
var CoderDevSetupFleet = []DevSetupFleetEntry{
	{Bot: NativeBotPlanRuntime, UseCase: "plan"},
	{Bot: NativeBotReplRuntime, UseCase: "repl"},
	{Bot: NativeBotReviewRuntime, UseCase: "review"},
	{Bot: NativeBotLeadRuntime, UseCase: "lead"},
	{Bot: "msgmate-coder-consolidate-bot", UseCase: "consolidate"},
	{Bot: "msgmate-coder-cleanup-bot", UseCase: "cleanup"},
	{
		// The runtime selector is dispatched across the whole development
		// fleet, so it pins the per-repo profile table explicitly instead of
		// relying on the public per-repo default.
		Bot:     "open-chat-ci-bot-runtime-selector-bot",
		UseCase: "runtime-select",
		Repos:   devSetupFleetDevelopmentRepos(),
		Config: map[string]interface{}{
			DevSetupConfigKey: map[string]interface{}{
				"profile":    DevSetupProfileDefault,
				"submodules": true,
				"profiles":   devSetupFleetRepoProfiles(),
			},
		},
	},
}

func devSetupFleetDevelopmentRepos() []string {
	repos := make([]string, 0, len(DefaultDevSetupProfiles))
	for _, entry := range DefaultDevSetupProfiles {
		repos = append(repos, entry.RepoPrefix)
	}
	sort.Strings(repos)
	return repos
}

func devSetupFleetRepoProfiles() map[string]string {
	out := map[string]string{}
	for _, entry := range DefaultDevSetupProfiles {
		out[entry.RepoPrefix] = entry.Profile
	}
	return out
}

// DevSetupFleetPlan is the resolved set-up of one fleet entry.
type DevSetupFleetPlan struct {
	// Bot is the ci.msgmate.io bot name.
	Bot string `json:"bot"`
	// UseCase is the coder stage the bot serves.
	UseCase string `json:"use_case"`
	// Explicit reports whether the bot pins the set-up itself.
	Explicit bool `json:"explicit"`
	// Status is one of the DevSetupFleetStatus* values.
	Status string `json:"status"`
	// Repos are the resolved per-repository plans.
	Repos []DevSetupRepoPlan `json:"repos"`
	// Gaps are the coverage defects of this entry.
	Gaps []string `json:"gaps,omitempty"`
}

// DevSetupFleetPlans resolves the set-up of every fleet entry. A bot that pins
// its own `coder_dev_setup` (per-use-case override path) is resolved from that
// config; every other bot resolves the per-repo default.
func DevSetupFleetPlans(entries []DevSetupFleetEntry) []DevSetupFleetPlan {
	out := make([]DevSetupFleetPlan, 0, len(entries))
	for _, entry := range entries {
		plan := DevSetupFleetPlan{Bot: strings.TrimSpace(entry.Bot), UseCase: strings.TrimSpace(entry.UseCase)}
		_, plan.Explicit = ResolveDevSetup(entry.Config)
		repos := entry.Repos
		if len(repos) == 0 {
			repos = []string{DevSetupFleetDefaultRepo}
		}
		for _, repo := range repos {
			plan.Repos = append(plan.Repos, DevSetupPlanForConfig(entry.Config, repo))
		}
		// DevSetupMatrixGaps already reports both failure modes the fleet cares
		// about: a repository no plan covers (half-set-up sandbox) and a plan
		// naming a profile outside the shipped catalogue.
		plan.Gaps = append(plan.Gaps, DevSetupMatrixGaps(plan.Repos)...)
		plan.Status = devSetupFleetStatus(plan)
		out = append(out, plan)
	}
	return out
}

func devSetupFleetStatus(plan DevSetupFleetPlan) string {
	if len(plan.Repos) == 0 {
		return DevSetupFleetStatusUnconfigured
	}
	enabled, disabled := 0, 0
	for _, repo := range plan.Repos {
		switch repo.PlanSource {
		case DevSetupPlanSourceUnconfigured:
			return DevSetupFleetStatusUnconfigured
		case DevSetupPlanSourceDisabled:
			disabled++
		default:
			if repo.PlanEnabled {
				enabled++
			}
		}
	}
	if enabled == 0 && disabled > 0 {
		return DevSetupFleetStatusDisabled
	}
	return DevSetupFleetStatusSatisfied
}

// DevSetupFleetGaps flattens the coverage defects of a plan list into the
// operator-facing strings the report and CI print.
func DevSetupFleetGaps(plans []DevSetupFleetPlan) []string {
	out := []string{}
	for _, plan := range plans {
		for _, gap := range plan.Gaps {
			out = append(out, plan.Bot+": "+gap)
		}
	}
	sort.Strings(out)
	return out
}

// RenderDevSetupFleetMarkdown renders the fleet table: one row per bot and
// repository, so the per-use-case / per-repo coverage is reviewable in a PR.
func RenderDevSetupFleetMarkdown(plans []DevSetupFleetPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Coder development set-up (ci#756, schema v%d)\n\n", DevSetupFleetVersion)
	b.WriteString("| Bot | Use-case | Source | Repository | Profile | Sandbox |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, plan := range plans {
		source := "per-repo default"
		if plan.Explicit {
			source = "explicit"
		}
		if len(plan.Repos) == 0 {
			fmt.Fprintf(&b, "| `%s` | %s | %s | — | — | — |\n", plan.Bot, plan.UseCase, source)
			continue
		}
		for _, repo := range plan.Repos {
			profile := repo.Profile
			if profile == "" {
				profile = "—"
			} else if !repo.PlanEnabled {
				profile += " (off)"
			}
			variant := repo.SandboxVariant
			if variant == "" {
				variant = SandboxDefaultVariant + " (default)"
			} else if !repo.SandboxVariantMapped {
				variant += " (default)"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | `%s` | %s | %s |\n",
				plan.Bot, plan.UseCase, source, repo.Repo, profile, variant)
		}
	}
	gaps := DevSetupFleetGaps(plans)
	fmt.Fprintf(&b, "\n## Coverage gaps: %d\n\n", len(gaps))
	if len(gaps) == 0 {
		b.WriteString("Every coder bot resolves a known-profile development set-up for every repository its use-case serves.\n")
		return b.String()
	}
	for _, gap := range gaps {
		fmt.Fprintf(&b, "- %s\n", gap)
	}
	return b.String()
}

// RenderDevSetupConfigFragment renders the non-secret `coder_dev_setup` block an
// interaction (or the ci.msgmate.io config delta) can embed to pin the set-up
// explicitly: the default profile plus the per-repo profile table. It contains
// repository slugs and profile names only.
func RenderDevSetupConfigFragment() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# ci#756 development set-up (schema v%d) — non-secret, no tokens/paths.\n", DevSetupFleetVersion)
	b.WriteString("# Merge into an interaction's shared_config (or a ci config delta).\n")
	fmt.Fprintf(&b, "%s:\n", DevSetupConfigKey)
	fmt.Fprintf(&b, "  profile: %s\n", DevSetupProfileDefault)
	b.WriteString("  submodules: true\n")
	b.WriteString("  profiles:\n")
	profiles := devSetupFleetRepoProfiles()
	repos := make([]string, 0, len(profiles))
	for repo := range profiles {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		fmt.Fprintf(&b, "    %s: %s\n", repo, profiles[repo])
	}
	fmt.Fprintf(&b, "  # fallback profile when the private manifest does not ship %s yet: %s\n",
		DevSetupProfileDefault, DevSetupProfileFallback)
	return b.String()
}
