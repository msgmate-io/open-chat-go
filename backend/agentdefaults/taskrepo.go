package agentdefaults

import "strings"

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
)

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
func ReconcileTaskRepo(config map[string]interface{}, tags []string, explicitToolInit map[string]interface{}) map[string]interface{} {
	taskRepo := TaskRepoFromTags(tags)
	if taskRepo == "" || config == nil {
		return config
	}
	stored := pinnedWorkspaceRepo(config)
	if RepoSlug(stored) == "" || RepoSlug(stored) == taskRepo {
		// Nothing pinned, or already the task repo: leave it to the ordinary
		// seeding path.
		return config
	}
	if RepoSlug(pinnedWorkspaceRepo(map[string]interface{}{ToolInitConfigKey: explicitToolInit})) != "" {
		// The caller pinned the workspace for this interaction explicitly.
		return config
	}

	out := make(map[string]interface{}, len(config))
	for key, value := range config {
		out[key] = value
	}
	toolInit, _ := config[ToolInitConfigKey].(map[string]interface{})
	reconciledInit := make(map[string]interface{}, len(toolInit))
	for key, value := range toolInit {
		reconciledInit[key] = value
	}
	selector, _ := toolInit[WorkspaceSelectorTool].(map[string]interface{})
	reconciledSelector := make(map[string]interface{}, len(selector))
	for key, value := range selector {
		reconciledSelector[key] = value
	}
	reconciledSelector[WorkspaceRepoConfigKey] = RepoPrefix(stored) + taskRepo
	reconciledInit[WorkspaceSelectorTool] = reconciledSelector
	out[ToolInitConfigKey] = reconciledInit
	return out
}
