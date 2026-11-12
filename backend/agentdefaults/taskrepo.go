package agentdefaults

import (
	"fmt"
	"strings"
)

// Task-repo reconciliation (ci#687, surfaced by the native-coder self-check
// suite ci#622).
//
// A coder interaction names the repository the agent must work in via the
// dispatch `repo:<owner/name>` tag. A bot - notably a role bot such as the
// development lead - additionally carries a *stored* workspace selection in its
// default_shared_config `tool_init.coder_select_workspace.git_repo`. That pin is
// written long before any single dispatch and therefore names the bot's own
// default repo.
//
// When both exist and disagree, the stored pin used to win, so the sandbox was
// provisioned with the wrong checkout (the self-check observed an agent working
// on - or honestly giving up on - a pre-selected monorepo instead of the task
// repo). A stored pin may only fill in a workspace when the dispatch names no
// task repo; when the dispatch names one, the task repo wins.
//
// The reconciliation is deliberately narrow:
//   - no dispatch task repo -> nothing to do;
//   - no stored pin -> nothing to do (the integration seeds the task repo);
//   - an *explicit* caller pin (request tool_init/config_overrides) is never
//     overridden - the caller is authoritative;
//   - everything is copied, the input config is never mutated.
const (
	// ToolInitConfigKey is the shared-config key holding the per-tool init
	// handshake payloads.
	ToolInitConfigKey = "tool_init"
	// WorkspaceSelectorTool is the coder tool whose init payload carries the
	// workspace (and therefore the repository) an interaction runs in.
	WorkspaceSelectorTool = "coder_select_workspace"
	// WorkspaceRepoConfigKey is the init payload key naming the git remote of
	// the workspace.
	WorkspaceRepoConfigKey = "git_repo"
	// TaskRepoTagPrefix marks a dispatch tag that names the task repository.
	TaskRepoTagPrefix = "repo:"
	// AutoSandboxConfigKey is the selector init key asking the sandbox backend
	// for a fresh per-chat workspace instead of reattaching an existing one.
	AutoSandboxConfigKey = "auto_sandbox"
)

// WorkspaceIdentityConfigKeys are the `coder_select_workspace` init payload keys
// that name an *already provisioned* sandbox workspace instead of a repository
// to clone. A stored role-bot pin may carry both a stale git_repo and a stale
// workspace identity; rewriting only git_repo would leave the identity in place
// and the selector would keep handing the agent the pre-selected monorepo
// checkout (ci#1048, the remaining half of ci#687).
var WorkspaceIdentityConfigKeys = []string{
	"workspace",
	"workspace_uuid",
	"workspace_id",
	"sandbox_workspace_uuid",
	"sandbox_uuid",
}

// TaskRepoFromTags returns the canonical `owner/name` slug named by the dispatch
// tags, or "" when the dispatch does not name a task repo.
func TaskRepoFromTags(tags []string) string {
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if !strings.HasPrefix(strings.ToLower(trimmed), TaskRepoTagPrefix) {
			continue
		}
		if slug := RepoSlug(trimmed[len(TaskRepoTagPrefix):]); slug != "" {
			return slug
		}
	}
	return ""
}

// RepoSlug reduces a repository reference (`owner/name`,
// `https://github.com/owner/name(.git)` or `git@github.com:owner/name.git`) to
// the canonical lower-case `owner/name` slug used for comparison.
func RepoSlug(reference string) string {
	trimmed := strings.TrimSpace(reference)
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, "://"); idx >= 0 {
		rest := trimmed[idx+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return ""
		}
		// drop the host: what remains starts at the owner.
		trimmed = rest[slash+1:]
	} else if colon := strings.Index(trimmed, ":"); colon >= 0 && !strings.Contains(trimmed[:colon], "/") {
		// scp-like syntax: git@github.com:owner/name.git
		trimmed = trimmed[colon+1:]
	}
	trimmed = strings.TrimSuffix(strings.Trim(trimmed, "/"), ".git")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		return ""
	}
	owner := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])
	if owner == "" || name == "" {
		return ""
	}
	return strings.ToLower(owner + "/" + name)
}

// pinnedWorkspaceRepo reads `tool_init.<selector>.git_repo` out of a config map.
func pinnedWorkspaceRepo(config map[string]interface{}) string {
	if config == nil {
		return ""
	}
	toolInit, ok := config[ToolInitConfigKey].(map[string]interface{})
	if !ok {
		return ""
	}
	selector, ok := toolInit[WorkspaceSelectorTool].(map[string]interface{})
	if !ok {
		return ""
	}
	repo, _ := selector[WorkspaceRepoConfigKey].(string)
	return repo
}

// RepoPrefix returns the URL prefix (scheme+host, or the scp-like `git@host:`)
// a repository reference was written with, so a rewritten pin keeps the host it
// was configured with instead of being forced onto github.com. A bare
// `owner/name` slug has no host, and defaults to https://github.com/.
func RepoPrefix(reference string) string {
	trimmed := strings.TrimSpace(reference)
	if trimmed == "" {
		return "https://github.com/"
	}
	if idx := strings.Index(trimmed, "://"); idx >= 0 {
		rest := trimmed[idx+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "https://github.com/"
		}
		return trimmed[:idx+3] + rest[:slash] + "/"
	}
	if colon := strings.Index(trimmed, ":"); colon >= 0 && !strings.Contains(trimmed[:colon], "/") {
		return trimmed[:colon+1]
	}
	return "https://github.com/"
}

// ReconcileTaskRepo rewrites a *stored* workspace pin that contradicts the task
// repository named by the dispatch tags (ci#687). The caller-pinned payload
// (the request's tool_init) is passed separately so an explicit selection is
// always left alone, and a config without a contradicting pin is returned
// unchanged.
//
// ci#1048: a stored pin that names a *provisioned workspace* (auto_sandbox
// selection, workspace_uuid, ...) is equally contradicting - the selector then
// hands the agent the pre-provisioned monorepo checkout and never clones the
// task repo, which is how the development-lead role failed the ci#622 selfcheck
// with `sandbox_misprovisioned_workspace`. Such a pin is reconciled by pinning
// the task repo and dropping the stale workspace identity, so provisioning
// clones the task repository instead of reattaching the wrong checkout.
func ReconcileTaskRepo(config map[string]interface{}, tags []string, explicitToolInit map[string]interface{}) map[string]interface{} {
	taskRepo := TaskRepoFromTags(tags)
	if taskRepo == "" || config == nil {
		return config
	}
	stored := pinnedWorkspaceRepo(config)
	storedSelector := selectorOf(config)
	explicitSelector := selectorOf(map[string]interface{}{ToolInitConfigKey: explicitToolInit})
	repoKnown := RepoSlug(stored) != ""
	if repoKnown && RepoSlug(stored) == taskRepo {
		// Already the task repo: leave it to the ordinary seeding path.
		return config
	}
	if !repoKnown && !pinsProvisionedWorkspace(storedSelector) {
		// Nothing pinned at all: the ordinary seeding path pins the task repo.
		return config
	}
	if pinsProvisionedWorkspace(explicitSelector) ||
		RepoSlug(pinnedWorkspaceRepo(map[string]interface{}{ToolInitConfigKey: explicitToolInit})) != "" {
		// The caller pinned the workspace for this interaction explicitly.
		return config
	}

	out := make(map[string]interface{}, len(config))
	for key, value := range config {
		out[key] = value
	}
	reconciledSelector := make(map[string]interface{}, len(storedSelector))
	for key, value := range storedSelector {
		reconciledSelector[key] = value
	}
	prefix := RepoPrefix(stored)
	if !repoKnown {
		prefix = RepoPrefix(taskRepo)
	}
	reconciledSelector[WorkspaceRepoConfigKey] = prefix + taskRepo
	// The stale identity is what keeps the agent attached to the wrong
	// checkout; the task repo pin above wins only if it is dropped.
	for _, key := range WorkspaceIdentityConfigKeys {
		delete(reconciledSelector, key)
	}
	reconciledInit := make(map[string]interface{}, len(toolInitOf(config)))
	toolInit, _ := config[ToolInitConfigKey].(map[string]interface{})
	for key, value := range toolInit {
		reconciledInit[key] = value
	}
	reconciledInit[WorkspaceSelectorTool] = reconciledSelector
	out[ToolInitConfigKey] = reconciledInit
	return out
}

// selectorOf returns the `tool_init.<selector>` sub-map of a config, or nil.
func selectorOf(config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	selector, _ := toolInitOf(config)[WorkspaceSelectorTool].(map[string]interface{})
	return selector
}

// toolInitOf returns the `tool_init` sub-map of a config, or nil.
func toolInitOf(config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	toolInit, _ := config[ToolInitConfigKey].(map[string]interface{})
	return toolInit
}

// pinsProvisionedWorkspace reports whether a workspace selector names an already
// provisioned sandbox workspace instead of a repository to clone: either an
// identity key is set, or the payload asks for `auto_sandbox` without naming a
// repository (the shape a role bot ends up with after it once selected a
// workspace by hand).
func pinsProvisionedWorkspace(selector map[string]interface{}) bool {
	if len(selector) == 0 {
		return false
	}
	for _, key := range WorkspaceIdentityConfigKeys {
		if value, ok := selector[key]; ok && strings.TrimSpace(stringOfAny(value)) != "" {
			return true
		}
	}
	if auto, ok := selector[AutoSandboxConfigKey]; ok && truthy(auto) &&
		strings.TrimSpace(stringOfAny(selector[WorkspaceRepoConfigKey])) == "" {
		return true
	}
	return false
}

// stringOfAny renders a JSON-ish selector value as a trimmed string.
func stringOfAny(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

// truthy reports whether a JSON-ish selector value means "yes".
func truthy(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		}
		return false
	case float64:
		return typed != 0
	case int:
		return typed != 0
	}
	return false
}
