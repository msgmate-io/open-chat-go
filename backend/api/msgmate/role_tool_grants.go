package msgmate

import (
	"strings"
)

// Role tool grants (ci#1189, "fix ci.msgmate.io plan bots").
//
// A role bot (the git integration's `plan`, `review`, `indexer`, `chooser`
// entries) is registered from a stored bootstrap config. Those stored configs
// drift: a plan bot shipped with only the read-only repository tools cannot
// actually plan, because it can neither read the GitHub issue it was dispatched
// for nor post its result or hand the ticket to an implementer.
//
// This file is the *minimum* capability each role must carry, expressed as data
// instead of being hand-repeated in every bootstrap YAML. The list is the
// authoritative one; bootstrap configs may select more, never less.
//
// Roles are grouped by what the capability is for:
//   - Read:    context the agent must be able to look up (the issue, the diff,
//     the CI run, the file it is asked about).
//   - Result:  the one sanctioned mutation that publishes the agent's output.
//   - Handoff: the tool that starts the next agent instead of leaving the work
//     parked on a human.
//
// The merge below is deliberately additive and idempotent: it never removes a
// tool a config already selects, never reorders the existing selection, and is
// a no-op for an unknown or empty role. Applying it on every interaction start
// is therefore safe for user bots too.
const (
	// RolePlan is the read-only planning role (`coder_mode: plan`).
	RolePlan = "plan"
	// RoleReview is the code-review role.
	RoleReview = "review"
	// RoleIndexer is the repository-indexing role.
	RoleIndexer = "indexer"
	// RoleChooser is the routing role that picks the next agent.
	RoleChooser = "chooser"
)

// RoleGitHubReadTools is the GitHub read surface a role bot needs to operate
// instead of guessing: view/list issues and PRs, read a file, read a CI run.
var RoleGitHubReadTools = []string{
	"github_issue_view",
	"github_issue_list",
	"github_pr_view",
	"github_file_read",
	"github_run_view",
}

// DispatchAgentToolName is the handoff tool (see
// tools/coder_dispatch_agent_tool.go): it starts the next agent for a ticket
// through the internal agent-dispatch path.
const DispatchAgentToolName = "coder_dispatch_agent"

// IssueCommentToolName is the sanctioned result-publication tool of a plan or
// review bot: the plan/review is posted on the ticket it was dispatched for.
const IssueCommentToolName = "github_issue_comment"

// RoleToolGrant is the minimum capability of one agent role.
type RoleToolGrant struct {
	// Role is the catalog role name, lower-case.
	Role string
	// ReadTools are the lookups the role must be able to make.
	ReadTools []string
	// ResultTools publish the role's output.
	ResultTools []string
	// HandoffTools start the next agent.
	HandoffTools []string
}

// RoleToolGrants is the role catalog. It is the single source of truth the
// bootstrap configs and the write-time validation read.
var RoleToolGrants = []RoleToolGrant{
	{
		Role:        RolePlan,
		ReadTools:   RoleGitHubReadTools,
		ResultTools: []string{IssueCommentToolName},
		HandoffTools: []string{
			DispatchAgentToolName,
		},
	},
	{
		Role:        RoleReview,
		ReadTools:   RoleGitHubReadTools,
		ResultTools: []string{IssueCommentToolName, "github_pr_review"},
	},
	{
		Role:        RoleIndexer,
		ReadTools:   RoleGitHubReadTools,
		ResultTools: []string{IssueCommentToolName},
	},
	{
		Role:      RoleChooser,
		ReadTools: RoleGitHubReadTools,
		HandoffTools: []string{
			DispatchAgentToolName,
		},
	},
}

// RoleGrantFor resolves a role name to its grant. The lookup trims and
// lower-cases the name so `Plan` and `plan` behave the same; an unknown or empty
// role reports false and grants nothing.
func RoleGrantFor(role string) (RoleToolGrant, bool) {
	name := strings.ToLower(strings.TrimSpace(role))
	if name == "" {
		return RoleToolGrant{}, false
	}
	for _, grant := range RoleToolGrants {
		if grant.Role == name {
			return grant, true
		}
	}
	return RoleToolGrant{}, false
}

// RequiredRoleTools is the flat minimum tool list of a role (read, then result,
// then handoff), de-duplicated case-insensitively. An unknown role yields nil.
func RequiredRoleTools(role string) []string {
	grant, ok := RoleGrantFor(role)
	if !ok {
		return nil
	}
	ordered := make([]string, 0, len(grant.ReadTools)+len(grant.ResultTools)+len(grant.HandoffTools))
	seen := make(map[string]bool, cap(ordered))
	for _, list := range [][]string{grant.ReadTools, grant.ResultTools, grant.HandoffTools} {
		for _, tool := range list {
			key := strings.ToLower(strings.TrimSpace(tool))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			ordered = append(ordered, tool)
		}
	}
	return ordered
}

// MissingRoleGrants returns the role's required tools that the given selection
// does not contain, in catalog order. Write-time validation (ci#1189 step 6)
// uses it to reject a role bot that would ship without its read, result or
// handoff capability instead of letting the config drift silently.
func MissingRoleGrants(role string, tools []string) []string {
	required := RequiredRoleTools(role)
	if len(required) == 0 {
		return nil
	}
	present := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if key := strings.ToLower(strings.TrimSpace(tool)); key != "" {
			present[key] = true
		}
	}
	missing := make([]string, 0, len(required))
	for _, tool := range required {
		if !present[strings.ToLower(tool)] {
			missing = append(missing, tool)
		}
	}
	return missing
}

// MergeRoleGrants returns the tool selection with the role's minimum grants
// added, plus a copy of toolInit. It is additive only: every configured tool is
// kept in its original order, only genuinely absent grants are appended, and an
// unknown or empty role returns the input unchanged (so a user bot is never
// granted a GitHub or dispatch tool by accident). The inputs are never mutated -
// callers apply the result to the stored config they are about to persist or
// resolve - so applying the merge twice is a no-op in value terms.
func MergeRoleGrants(role string, tools []string, toolInit map[string]interface{}) ([]string, map[string]interface{}) {
	merged := make([]string, 0, len(tools)+len(RoleGitHubReadTools))
	seen := make(map[string]bool, cap(merged))
	for _, tool := range tools {
		if key := strings.ToLower(strings.TrimSpace(tool)); key != "" {
			seen[key] = true
		}
		merged = append(merged, tool)
	}
	for _, tool := range RequiredRoleTools(role) {
		if seen[strings.ToLower(tool)] {
			continue
		}
		seen[strings.ToLower(tool)] = true
		merged = append(merged, tool)
	}
	initCopy := make(map[string]interface{}, len(toolInit))
	for key, value := range toolInit {
		initCopy[key] = value
	}
	return merged, initCopy
}
