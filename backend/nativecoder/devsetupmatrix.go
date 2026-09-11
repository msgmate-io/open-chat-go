package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the audit surface of the ci#756 development set-up (devsetup.go):
// per configured repository it resolves what a ci.msgmate.io coding interaction
// actually gets — the sandbox palette variant and the development set-up plan —
// and it names the repositories that would still start half-set-up.
//
// `coder_dev_setup` is opt-in per bot/trigger and the built-in default only
// covers the known development repositories (DefaultDevSetupProfiles). A bot or
// trigger pointed at a repository outside that table silently clones a bare
// checkout — no submodules, no private CI tooling, no Helm/mobile checkouts —
// and the model then installs tooling ad hoc. The matrix makes that visible
// before a chat is opened:
//
//	plans := nativecoder.DevSetupMatrixPlan(configuredRepos)
//	problems := nativecoder.ValidateDevSetupMatrix(plans) // defects
//	gaps := nativecoder.DevSetupMatrixGaps(plans)         // repos without a plan
//
// For a repository the table does cover, the generated sandbox set-up script
// pre-pulls the repo's submodules up front (DevSetupScript: the `git submodule
// update --init --recursive` block right after the sandbox environment lines)
// so the integration profile set-up can materialize the batteries-included
// checkouts — the private CI tooling, the Helm chart and the mobile client —
// before the LLM starts working.
// RenderDevSetupMatrixMarkdown renders the same data as the operator-facing
// table, and the unit test keeps the public profile list, the per-repo default
// table and the sandbox palette from drifting apart. Like the sibling plan
// registries this is data-only: repository slugs, profile names and
// descriptions, never a token, kubeconfig or checkout path of a user.
const (
	// DevSetupMatrixVersion is the schema version of the matrix report.
	DevSetupMatrixVersion = 1

	// DevSetupPlanSourceDefault marks a plan the per-repo default supplied.
	DevSetupPlanSourceDefault = "default"
	// DevSetupPlanSourceExplicit marks a plan the interaction's own
	// `coder_dev_setup` declaration supplied.
	DevSetupPlanSourceExplicit = "explicit"
	// DevSetupPlanSourceDisabled marks a plan the configuration switched off
	// (`coder_dev_setup: false` / `enabled: false`).
	DevSetupPlanSourceDisabled = "disabled"
	// DevSetupPlanSourceUnconfigured marks a repository no plan covers.
	DevSetupPlanSourceUnconfigured = "unconfigured"
)

// DevSetupKnownProfiles is the public catalogue of development set-up profiles
// the generated script may name. It mirrors the profiles DEVELOPMENT.md and
// profile_setup.yaml document plus the private membership the `ci` repository
// ships; a profile outside this list is a configuration defect, not a
// fallback, because the generated `openchat-integrations prepare --profile`
// call would fail on it.
var DevSetupKnownProfiles = []string{
	"core-only",
	"default",
	"full",
	DevSetupProfileFallback,
	"full-android",
	DevSetupProfileDefault,
}

// DevSetupRepoPlan is the resolved development set-up of one repository: what a
// ci.msgmate.io coding interaction on it gets.
type DevSetupRepoPlan struct {
	// Repo is the normalized `owner/name` slug.
	Repo string `json:"repo"`
	// SandboxVariant is the palette variant the repository maps to.
	SandboxVariant string `json:"sandbox_variant,omitempty"`
	// SandboxVariantMapped reports whether the palette has an explicit trigger
	// for the repository (false means it falls back to the default variant).
	SandboxVariantMapped bool `json:"sandbox_variant_mapped"`
	// PlanEnabled reports whether a development set-up plan applies at all.
	PlanEnabled bool `json:"plan_enabled"`
	// PlanSource is one of the DevSetupPlanSource* values.
	PlanSource string `json:"plan_source"`
	// Profile is the integration profile the plan prepares.
	Profile string `json:"profile,omitempty"`
	// Submodules reports whether the plan pulls the repository's submodules.
	Submodules bool `json:"submodules,omitempty"`
	// ExtraRepos are repositories cloned next to the checkout.
	ExtraRepos []string `json:"extra_repos,omitempty"`
}

// DevSetupKnownProfile reports whether a profile belongs to the shipped
// catalogue. Unknown profiles are refused so a typo can never reach the sandbox
// script.
func DevSetupKnownProfile(profile string) bool {
	normalized := normalizeDevSetupProfile(profile)
	if normalized == "" {
		return false
	}
	for _, known := range DevSetupKnownProfiles {
		if known == normalized {
			return true
		}
	}
	return false
}

// DevSetupRepoPlanFor resolves the built-in plan for a repository. It is the
// matrix counterpart of DevSetupSpecForRepo: a repository outside the per-repo
// table yields PlanSource=unconfigured.
func DevSetupRepoPlanFor(repo string) DevSetupRepoPlan {
	plan := DevSetupRepoPlan{Repo: NormalizeRepo(repo)}
	if plan.Repo == "" {
		return plan
	}
	if variant, mapped := SandboxVariantForRepo(plan.Repo); variant != "" {
		plan.SandboxVariant, plan.SandboxVariantMapped = variant, mapped
	}
	spec := DevSetupSpecForRepo(plan.Repo)
	plan.PlanEnabled = spec.Enabled
	plan.PlanSource = DevSetupPlanSourceUnconfigured
	if !spec.Enabled {
		return plan
	}
	plan.PlanSource = DevSetupPlanSourceDefault
	plan.Profile = spec.Profile
	plan.Submodules = spec.Submodules
	plan.ExtraRepos = normalizedRepos(spec.ExtraRepos)
	return plan
}

// DevSetupPlanForConfig resolves the plan an interaction configuration actually
// produces for a repository, with the same precedence WithDevSetup applies: an
// explicit `coder_dev_setup` declaration (including its per-repo `profiles`
// table) always wins, a declaration that switches the set-up off wins too, and
// a configuration without a declaration keeps the per-repo default. A nil
// configuration is treated like an empty one.
func DevSetupPlanForConfig(config map[string]interface{}, repo string) DevSetupRepoPlan {
	plan := DevSetupRepoPlanFor(repo)
	if config == nil {
		return plan
	}
	_, declared := config[DevSetupConfigKey]
	spec, explicit := ResolveDevSetup(config)
	switch {
	case explicit:
		spec = spec.forRepo(plan.Repo)
		plan.PlanEnabled = spec.Enabled
		plan.PlanSource = DevSetupPlanSourceExplicit
		plan.Profile = spec.Profile
		plan.Submodules = spec.Submodules
		plan.ExtraRepos = normalizedRepos(spec.ExtraRepos)
	case declared:
		plan.PlanEnabled = false
		plan.PlanSource = DevSetupPlanSourceDisabled
		plan.Profile = ""
		plan.Submodules = false
		plan.ExtraRepos = nil
	}
	return plan
}

// DevSetupMatrixPlan resolves the built-in plan of every configured repository,
// deduplicated and sorted, so a report is stable across runs.
func DevSetupMatrixPlan(repos []string) []DevSetupRepoPlan {
	seen := make(map[string]bool, len(repos))
	plans := make([]DevSetupRepoPlan, 0, len(repos))
	for _, repo := range repos {
		normalized := NormalizeRepo(repo)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		plans = append(plans, DevSetupRepoPlanFor(normalized))
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Repo < plans[j].Repo })
	return plans
}

// DevSetupMatrixGaps returns the configured repositories that would still start
// half-set-up, in plan order: a repository without any plan, and a plan that
// names a profile outside DevSetupKnownProfiles. Unlike ValidateDevSetupMatrix
// this is a configuration to-do list, not a defect list.
func DevSetupMatrixGaps(plans []DevSetupRepoPlan) []string {
	var gaps []string
	for _, plan := range plans {
		if plan.Repo == "" {
			continue
		}
		if !plan.PlanEnabled {
			gaps = append(gaps, fmt.Sprintf("%s: no development set-up plan; a coder interaction clones the checkout without submodules, the private CI tooling or the Helm/mobile checkouts (declare the %s key on the bot/trigger or interaction)", plan.Repo, DevSetupConfigKey))
			continue
		}
		if !DevSetupKnownProfile(plan.Profile) {
			gaps = append(gaps, fmt.Sprintf("%s: development set-up profile %q is not part of DevSetupKnownProfiles", plan.Repo, plan.Profile))
		}
	}
	return gaps
}

// ValidateDevSetupMatrix returns the internal problems of a resolved matrix,
// empty when every entry is consistent. These are defects (an enabled plan
// without a profile, an unknown profile, an unusable or colliding extra
// repository) rather than missing configuration.
func ValidateDevSetupMatrix(plans []DevSetupRepoPlan) []string {
	var problems []string
	for _, plan := range plans {
		if plan.Repo == "" {
			problems = append(problems, "matrix entry without a repository")
			continue
		}
		if !plan.PlanEnabled {
			if plan.Profile != "" || plan.Submodules || len(plan.ExtraRepos) > 0 {
				problems = append(problems, fmt.Sprintf("%s: a disabled plan still carries a profile, submodules or checkouts", plan.Repo))
			}
			continue
		}
		if plan.Profile == "" {
			problems = append(problems, fmt.Sprintf("%s: enabled development set-up without a profile", plan.Repo))
		} else if !DevSetupKnownProfile(plan.Profile) {
			problems = append(problems, fmt.Sprintf("%s: unknown development set-up profile %q", plan.Repo, plan.Profile))
		}
		switch plan.PlanSource {
		case DevSetupPlanSourceDefault, DevSetupPlanSourceExplicit:
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown plan source %q", plan.Repo, plan.PlanSource))
		}
		dirs := make(map[string]bool, len(plan.ExtraRepos))
		for _, extra := range plan.ExtraRepos {
			dir := devSetupRepoDir(extra)
			if dir == "" {
				problems = append(problems, fmt.Sprintf("%s: extra repository %q is not a usable owner/name slug", plan.Repo, extra))
				continue
			}
			if dirs[dir] {
				problems = append(problems, fmt.Sprintf("%s: two extra repositories collide on the checkout directory %q", plan.Repo, dir))
			}
			dirs[dir] = true
		}
	}
	return problems
}

// RenderDevSetupMatrixMarkdown renders the matrix as the operator-facing
// documentation snippet: one row per configured repository plus the gaps that
// still need a `coder_dev_setup` declaration.
func RenderDevSetupMatrixMarkdown(plans []DevSetupRepoPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Development set-up matrix (ci#756)\n\n")
	fmt.Fprintf(&b, "Schema version: %d. Resolved from the per-repository defaults and the interaction's `%s` declaration.\n\n", DevSetupMatrixVersion, DevSetupConfigKey)
	b.WriteString("| repository | sandbox variant | profile | plan | submodules | extra checkouts |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, plan := range plans {
		variant := "-"
		if plan.SandboxVariant != "" {
			variant = "`" + plan.SandboxVariant + "`"
			if !plan.SandboxVariantMapped {
				variant += " (default)"
			}
		}
		profile := "-"
		if plan.Profile != "" {
			profile = "`" + plan.Profile + "`"
		}
		submodules := "no"
		if plan.Submodules {
			submodules = "yes"
		}
		extra := "-"
		if len(plan.ExtraRepos) > 0 {
			extra = "`" + strings.Join(plan.ExtraRepos, "`, `") + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n", plan.Repo, variant, profile, plan.PlanSource, submodules, extra)
	}
	gaps := DevSetupMatrixGaps(plans)
	if len(gaps) == 0 {
		b.WriteString("\nEvery configured repository resolves a development set-up plan.\n")
		return b.String()
	}
	b.WriteString("\n## Gaps\n\n")
	for _, gap := range gaps {
		b.WriteString("- " + gap + "\n")
	}
	return b.String()
}
