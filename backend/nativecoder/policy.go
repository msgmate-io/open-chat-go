// Package nativecoder holds the core-side pieces of the native msgmate_coder
// development loop that open-chat-go itself owns.
//
// The package is deliberately free of integration imports so it can be unit
// tested without an integration profile, and so every build (core-only or full)
// carries the same consolidation policy.
//
// This file owns the auto-consolidation policy (ci#653): the shared-config
// block `auto_consolidation` that makes a native coder agent roll its own ready
// pull requests up into exactly one `chore/consolidate-<date>` PR, have it
// merged through the existing auto-approve path (ci#538) once every required
// check is green, and roll later PRs into the NEXT consolidation (ci#652).
package nativecoder

import (
	"strconv"
	"strings"
)

// AutoConsolidationConfigKey is the bot/interaction shared-config key that
// carries the auto-consolidation policy. It is defaulted ON for native coder
// agents (see ApplySharedConfigDefaults) unless a bot config sets it explicitly
// (eg `"auto_consolidation": {"enabled": false}`).
const AutoConsolidationConfigKey = "auto_consolidation"

// AutoConsolidationEnvKey is the deployment-wide kill switch for the default.
// Set it to 0/false/off/no/disabled to roll the default back fleet-wide; an
// explicit per-agent shared-config block still wins for that agent.
const AutoConsolidationEnvKey = "OCI_MSGMATE_CODER_AUTO_CONSOLIDATION"

// Defaults for the auto-consolidation policy. They mirror the ci#653 design and
// are intentionally conservative: consolidate as soon as a single PR is ready,
// never include a failing/conflicting/draft PR, and keep exactly one
// consolidation PR open.
const (
	// DefaultAutoConsolidationBase is the branch a consolidation PR targets.
	// `dev` is the integration trunk (ci#833, ci#931): feature and consolidation
	// PRs land on `dev`, and the automatic `dev` -> `main` promotion PR is the
	// only deployment hop.
	DefaultAutoConsolidationBase = "dev"
	// DefaultAutoConsolidationScope restricts the policy to the repository the
	// agent works in (an optional repo allow-list narrows it further).
	DefaultAutoConsolidationScope = "repo"
	// DefaultAutoConsolidationBranchPrefix is the head-branch prefix of a
	// consolidation PR. The ci#538 auto-approve workflow only acts on
	// `chore/consolidate-*` PRs.
	DefaultAutoConsolidationBranchPrefix = "chore/consolidate"
	// DefaultAutoConsolidationThreshold is how many ready PRs are needed before
	// a consolidation PR is opened.
	DefaultAutoConsolidationThreshold = 1
	// AuthorSelf selects the agent's own GitHub identity as the only allowed
	// PR author.
	AuthorSelf = "self"
	// AuthorAny accepts PRs from any author.
	AuthorAny = "*"
)

// DefaultAutoConsolidationLabels is the standard label set a consolidation PR
// carries. A consolidation PR targets `dev` (the integration trunk, ci#833 /
// ci#931) and carries only the lightweight verification labels `test` and `e2e`;
// the heavy post-deploy suites (`deploy`, `sandbox-test`, `build`,
// `test-core-only`, `ai-test-mock`, `ai-test-real`) belong exclusively to the
// automatic `dev` -> `main` promotion PR (see DEVELOPMENT.md, "Pull-request CI
// labels and deployment policy").
var DefaultAutoConsolidationLabels = []string{
	"test",
	"e2e",
}

// AutoConsolidationPolicy is the resolved auto-consolidation policy of a native
// coder agent.
type AutoConsolidationPolicy struct {
	// Enabled turns the whole feature on/off (default true).
	Enabled bool `json:"enabled"`
	// Threshold is the number of ready PRs that opens a consolidation PR.
	Threshold int `json:"threshold"`
	// Base is the branch the consolidation PR targets.
	Base string `json:"base"`
	// Scope is `repo` (this repository only) — the pull-request facts are
	// filtered to Base/Repo before they reach the planner.
	Scope string `json:"scope"`
	// Repos is an optional allow-list of `owner/name` repositories. When empty
	// the repository the facts belong to is used.
	Repos []string `json:"repos,omitempty"`
	// Authors is the allow-list of PR authors. `self` means the agent's own
	// GitHub login, `*` accepts any author.
	Authors []string `json:"authors"`
	// RequireGreen excludes PRs whose checks are not green, that are
	// conflicting/unmergeable or whose mergeability is still unknown.
	RequireGreen bool `json:"require_green"`
	// RequireIssueLink makes every consolidated PR implement exactly one leaf
	// issue in the private tracker (ci#813). A PR without a standalone
	// `Closes msgmate-io/open-chat-go-ci#N` line is never consolidated.
	RequireIssueLink bool `json:"require_issue_link"`
	// Labels is the label set applied to the consolidation PR.
	Labels []string `json:"labels"`
	// BranchPrefix is the consolidation head-branch prefix.
	BranchPrefix string `json:"branch_prefix"`
	// OneOpenAtATime keeps exactly one consolidation PR open: later ready PRs
	// roll into the NEXT consolidation (ci#652).
	OneOpenAtATime bool `json:"one_open_at_a_time"`
	// RollupIssue maintains the consolidation tracker issue (ci#415).
	RollupIssue bool `json:"rollup_issue"`
}

// DefaultAutoConsolidationPolicy returns the default-ON policy of ci#653.
func DefaultAutoConsolidationPolicy() AutoConsolidationPolicy {
	return AutoConsolidationPolicy{
		Enabled:          true,
		Threshold:        DefaultAutoConsolidationThreshold,
		Base:             DefaultAutoConsolidationBase,
		Scope:            DefaultAutoConsolidationScope,
		Authors:          []string{AuthorSelf},
		RequireGreen:     true,
		RequireIssueLink: true,
		Labels:           append([]string(nil), DefaultAutoConsolidationLabels...),
		BranchPrefix:     DefaultAutoConsolidationBranchPrefix,
		OneOpenAtATime:   true,
		RollupIssue:      true,
	}
}

// AutoConsolidationPolicyMap renders the default policy as the JSON object that
// is stored in a bot's shared config, so the resolved defaults travel with the
// agent's config and stay reviewable in the deployment config.
func AutoConsolidationPolicyMap() map[string]interface{} {
	return map[string]interface{}{
		"enabled":            true,
		"threshold":          DefaultAutoConsolidationThreshold,
		"base":               DefaultAutoConsolidationBase,
		"scope":              DefaultAutoConsolidationScope,
		"authors":            []interface{}{AuthorSelf},
		"require_green":      true,
		"require_issue_link": true,
		"labels":             stringSliceToInterface(DefaultAutoConsolidationLabels),
		"branch_prefix":      DefaultAutoConsolidationBranchPrefix,
		"one_open_at_a_time": true,
		"rollup_issue":       true,
	}
}

// ResolveAutoConsolidationPolicy parses the raw `auto_consolidation`
// shared-config value into a policy. Every field is optional and an unset or
// invalid field falls back to its default; the accepted shorthands are a
// boolean (`true`/`false`) and a boolean-like string (`"off"`).
func ResolveAutoConsolidationPolicy(raw interface{}) AutoConsolidationPolicy {
	policy := DefaultAutoConsolidationPolicy()
	if raw == nil {
		return policy
	}

	switch value := raw.(type) {
	case bool:
		policy.Enabled = value
		return policy
	case string:
		policy.Enabled = parseBoolish(value, policy.Enabled)
		return policy
	case map[string]interface{}:
		return applyAutoConsolidationMap(policy, value)
	default:
		return policy
	}
}

// applyAutoConsolidationMap overlays the explicitly configured keys of a policy
// map on top of the defaults.
func applyAutoConsolidationMap(policy AutoConsolidationPolicy, raw map[string]interface{}) AutoConsolidationPolicy {
	for key, value := range raw {
		switch normalizePolicyKey(key) {
		case "enabled":
			policy.Enabled = boolishValue(value, policy.Enabled)
		case "threshold":
			if parsed, ok := intValue(value); ok {
				policy.Threshold = parsed
			}
		case "base":
			if parsed := stringValue(value); parsed != "" {
				policy.Base = parsed
			}
		case "scope":
			if parsed := stringValue(value); parsed != "" {
				policy.Scope = parsed
			}
		case "repos":
			if parsed := stringListValue(value); parsed != nil {
				policy.Repos = parsed
			}
		case "repo":
			// A single-repository shorthand for `repos: [<repo>]`.
			if parsed := stringValue(value); parsed != "" {
				policy.Repos = []string{parsed}
			}
		case "authors":
			if parsed := stringListValue(value); len(parsed) > 0 {
				policy.Authors = parsed
			}
		case "require_green":
			policy.RequireGreen = boolishValue(value, policy.RequireGreen)
		case "require_issue_link":
			policy.RequireIssueLink = boolishValue(value, policy.RequireIssueLink)
		case "labels":
			if parsed := stringListValue(value); len(parsed) > 0 {
				policy.Labels = parsed
			}
		case "branch_prefix":
			if parsed := stringValue(value); parsed != "" {
				policy.BranchPrefix = parsed
			}
		case "one_open_at_a_time":
			policy.OneOpenAtATime = boolishValue(value, policy.OneOpenAtATime)
		case "rollup_issue":
			policy.RollupIssue = boolishValue(value, policy.RollupIssue)
		}
	}
	return normalizePolicy(policy)
}

// normalizePolicy clamps a policy into a usable shape: a threshold of at least
// one, a non-empty base/scope/branch prefix, at least one author and the
// standard label set when none is configured.
func normalizePolicy(policy AutoConsolidationPolicy) AutoConsolidationPolicy {
	if policy.Threshold < 1 {
		policy.Threshold = DefaultAutoConsolidationThreshold
	}
	policy.Base = strings.TrimSpace(policy.Base)
	if policy.Base == "" {
		policy.Base = DefaultAutoConsolidationBase
	}
	policy.Scope = strings.ToLower(strings.TrimSpace(policy.Scope))
	if policy.Scope == "" {
		policy.Scope = DefaultAutoConsolidationScope
	}
	policy.BranchPrefix = strings.Trim(strings.TrimSpace(policy.BranchPrefix), "/")
	if policy.BranchPrefix == "" {
		policy.BranchPrefix = DefaultAutoConsolidationBranchPrefix
	}
	policy.Authors = normalizeStringList(policy.Authors)
	if len(policy.Authors) == 0 {
		policy.Authors = []string{AuthorSelf}
	}
	policy.Repos = normalizeStringList(policy.Repos)
	policy.Labels = normalizeStringList(policy.Labels)
	if len(policy.Labels) == 0 {
		policy.Labels = append([]string(nil), DefaultAutoConsolidationLabels...)
	}
	return policy
}

// normalizePolicyKey lowercases a policy key and folds the handful of aliases
// the deployment config uses.
func normalizePolicyKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "enable":
		return "enabled"
	case "branches", "branch":
		return "branch_prefix"
	case "consolidate_threshold", "min_ready":
		return "threshold"
	default:
		return normalized
	}
}

// normalizeStringList trims, lowercases (only for branch/repo-like values the
// caller keeps as-is) and de-duplicates a string list while dropping empties.
// Values keep their case; comparison is always case-insensitive.
func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

// stringValue accepts a string or any JSON scalar rendered as text.
func stringValue(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return ""
	}
}

// stringListValue accepts a list of strings, a single string or a
// comma-separated string. It returns nil when nothing usable was provided.
func stringListValue(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return normalizeStringList(typed)
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				out = append(out, text)
			}
		}
		return normalizeStringList(out)
	case string:
		return normalizeStringList(strings.Split(typed, ","))
	default:
		return nil
	}
}

// intValue accepts a JSON number, a float or a numeric string.
func intValue(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// boolishValue accepts a JSON boolean or a boolean-like string, falling back to
// the provided default for anything else.
func boolishValue(value interface{}, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return parseBoolish(typed, fallback)
	default:
		return fallback
	}
}

// parseBoolish parses the boolean-like strings used in shared configs and
// deployment deltas.
func parseBoolish(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled", "enable":
		return true
	case "0", "false", "no", "off", "disabled", "disable":
		return false
	default:
		return fallback
	}
}

func stringSliceToInterface(values []string) []interface{} {
	out := make([]interface{}, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

// AutoConsolidationEnvDisabled reports whether a deployment-wide kill-switch
// value disables the default. An empty value keeps the default ON.
func AutoConsolidationEnvDisabled(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	return !parseBoolish(value, true)
}
