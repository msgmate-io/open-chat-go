package nativecoder

import "strings"

// ci#413: per-repository auto_sandbox hand-off.
//
// A native coder interaction runs in its own disposable per-chat sandbox that
// clones the repository named by tool_init.coder_select_workspace.git_repo.
// Bots preset that value to their *bot-wide* default checkout (eg the
// open-chat-go monorepo), so a git trigger or dispatch that was invoked for
// another repository still ran against the default one.
//
// This file is the core-side half of the hand-off: the dispatch path resolves
// the repository the interaction was invoked for (an explicit `repo:<owner/name>`
// tag, a per-chat config repo key, or the trigger's repo URL) and binds it into
// the interaction's tool_init as the automatic sandbox clone target. The
// msgmate-coder integration keeps owning the actual sandbox provisioning and
// only ever sees a fully resolved git_repo.

const (
	// AutoSandboxToolInitKey is the chat-init tool whose inputs an auto_sandbox
	// run is provisioned from (the same entry every native coder agent carries).
	AutoSandboxToolInitKey = coderToolInitSelectWorkspace
	// AutoSandboxGitRepoInput is the input naming the repository the automatic
	// per-chat sandbox clones.
	AutoSandboxGitRepoInput = "git_repo"
	// AutoSandboxGitBranchInput is the input naming the branch the automatic
	// per-chat sandbox checks out once it has cloned that repository.
	AutoSandboxGitBranchInput = "git_branch"
	// AutoSandboxFlagInput is the input that switches the automatic per-chat
	// sandbox (clone + provision) on for the interaction.
	AutoSandboxFlagInput = "auto_sandbox"
)

// RepoValueFromTags returns the raw repository value carried by the first
// `repo:<...>` tag, or "" when no tag names one. The value is returned as the
// caller wrote it (an `owner/name` slug or a clone remote) so the caller can
// decide how to canonicalise it.
func RepoValueFromTags(tags []string) string {
	return tagValueWithPrefix(tags, RepoTagPrefix)
}

// BranchValueFromTags returns the raw branch value carried by the first
// `branch:<name>` tag, or "" when no tag names one.
func BranchValueFromTags(tags []string) string {
	return tagValueWithPrefix(tags, BranchTagPrefix)
}

// tagValueWithPrefix returns the first non-empty value of a prefixed tag
// (`repo:<value>`, `branch:<value>`), comparing the prefix case-insensitively
// and ignoring surrounding whitespace on both sides.
func tagValueWithPrefix(tags []string, prefix string) string {
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if len(trimmed) <= len(prefix) {
			continue
		}
		if !strings.EqualFold(trimmed[:len(prefix)], prefix) {
			continue
		}
		if value := strings.TrimSpace(trimmed[len(prefix):]); value != "" {
			return value
		}
	}
	return ""
}

// GitRemoteForRepo renders a repository reference as a clone remote. An
// explicit URL or scp-style remote is passed through unchanged, a bare
// `owner/name` slug is expanded to its GitHub remote (the host the coder bots
// work against), and a value that names no usable repository is reported as ""
// so callers leave the configured default checkout alone instead of cloning a
// guess.
func GitRemoteForRepo(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") || (strings.Contains(trimmed, "@") && strings.Contains(trimmed, ":")) {
		return trimmed
	}
	repo := NormalizeRepo(trimmed)
	if repo == "" {
		return ""
	}
	return "https://github.com/" + repo + ".git"
}

// AutoSandboxToolInitRepo returns the clone target already declared by the given
// explicit tool_init (the caller's own coder_select_workspace.git_repo), or ""
// when the caller did not pin one.
func AutoSandboxToolInitRepo(toolInit map[string]interface{}) string {
	workspaceInit, ok := toolInit[AutoSandboxToolInitKey].(map[string]interface{})
	if !ok {
		return ""
	}
	value, _ := workspaceInit[AutoSandboxGitRepoInput].(string)
	return strings.TrimSpace(value)
}

// ExplicitAutoSandboxRepo reports the repository the *caller* pinned for this
// interaction's automatic sandbox: a git_repo in the explicit tool_init, or a
// repo key in the explicit config overrides. "" means the sandbox repository is
// only a bot/integration default and may be re-bound to the dispatched repo.
func ExplicitAutoSandboxRepo(toolInit, configOverrides map[string]interface{}) string {
	if repo := AutoSandboxToolInitRepo(toolInit); repo != "" {
		return repo
	}
	return repoFromConfig(configOverrides)
}

// AutoSandboxRepoTarget returns the repository this interaction should run its
// automatic sandbox against: an explicit `repo:<...>` tag always wins, otherwise
// the repository declared by the effective shared config (repoConfigKeys,
// including the bot's default). An empty result means "keep the configured
// default checkout".
func AutoSandboxRepoTarget(config map[string]interface{}, tags []string) string {
	if value := RepoValueFromTags(tags); value != "" {
		return value
	}
	return repoFromConfig(config)
}

// AutoSandboxRepoForInteraction is the dispatch-level selection used by the core
// when it creates a coder interaction. A repository the caller pinned for this
// interaction (tool_init.coder_select_workspace.git_repo or a repo key in
// config_overrides) is authoritative and is never re-bound; otherwise an
// explicit `repo:<...>` tag wins, falling back to the effective config's repo
// key (the bot/trigger default). "" means the caller did not ask for a specific
// repository and the configured checkout stays as-is.
func AutoSandboxRepoForInteraction(config map[string]interface{}, tags []string, toolInit, configOverrides map[string]interface{}) string {
	if ExplicitAutoSandboxRepo(toolInit, configOverrides) != "" {
		return ""
	}
	return AutoSandboxRepoTarget(config, tags)
}

// ExplicitAutoSandboxBranch reports the branch the *caller* pinned for this
// interaction's automatic sandbox: a git_branch in the explicit tool_init.
// "" means the sandbox branch is only inherited from a bot/trigger default and
// may be re-bound to the dispatched branch.
func ExplicitAutoSandboxBranch(toolInit map[string]interface{}) string {
	workspaceInit, ok := toolInit[AutoSandboxToolInitKey].(map[string]interface{})
	if !ok {
		return ""
	}
	value, _ := workspaceInit[AutoSandboxGitBranchInput].(string)
	return strings.TrimSpace(value)
}

// AutoSandboxBranchForInteraction is the dispatch-level branch selection used by
// the core when it creates a coder interaction. A branch the caller pinned for
// this interaction (tool_init.coder_select_workspace.git_branch) is authoritative
// and is never re-bound; otherwise an explicit `branch:<name>` tag wins. "" means
// the caller did not ask for a specific branch and the sandbox keeps whatever
// branch the configured checkout already uses.
func AutoSandboxBranchForInteraction(tags []string, toolInit map[string]interface{}) string {
	if ExplicitAutoSandboxBranch(toolInit) != "" {
		return ""
	}
	return BranchValueFromTags(tags)
}

// refsHeadsPrefix is the `refs/heads/` prefix a CI payload may keep on a branch
// name; it is stripped before the branch is used as a checkout target.
const refsHeadsPrefix = "refs/heads/"

// canonicalSandboxBranch turns a raw branch value into the checkout target used
// by the automatic per-chat sandbox. Unlike NormalizeBranch (the dedupe/tag
// identity, which lower-cases because it only ever has to compare), a checkout
// target must keep its case: git branch names are case-sensitive, so a
// `Feat/X` branch has to be fetched as `Feat/X`. The `refs/heads/` prefix and
// surrounding whitespace are stripped, and a value that names no usable branch
// is reported as "" so callers leave the checkout alone.
func canonicalSandboxBranch(branch string) string {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, refsHeadsPrefix):
		trimmed = strings.TrimSpace(trimmed[len(refsHeadsPrefix):])
	case lower == strings.TrimSuffix(refsHeadsPrefix, "/"):
		trimmed = ""
	}
	if trimmed == "" || strings.ContainsAny(trimmed, " \t") {
		return ""
	}
	return trimmed
}

// BindAutoSandboxBranch returns tool_init whose automatic per-chat sandbox
// checks out branch. changed reports whether anything was altered and the
// returned map is the caller's map when it was not, so a caller can cheaply keep
// the previous value.
//
// It only ever touches tool_init whose coder_select_workspace entry switches
// auto_sandbox on, so a fixed/pinned workspace and every non-coder bot keep
// exactly the inputs they were created with. A branch the caller pinned itself is
// the caller's responsibility (see ExplicitAutoSandboxBranch). Every other
// tool_init input is preserved and the caller's maps are copied, never mutated.
func BindAutoSandboxBranch(toolInit map[string]interface{}, branch string) (result map[string]interface{}, changed bool) {
	normalized := canonicalSandboxBranch(branch)
	if normalized == "" || toolInit == nil {
		return toolInit, false
	}
	existing, ok := toolInit[AutoSandboxToolInitKey].(map[string]interface{})
	if !ok || !autoSandboxEnabled(existing) {
		return toolInit, false
	}
	if current, ok := existing[AutoSandboxGitBranchInput].(string); ok && strings.TrimSpace(current) == normalized {
		return toolInit, false
	}

	workspaceInit := make(map[string]interface{}, len(existing)+1)
	for key, value := range existing {
		workspaceInit[key] = value
	}
	workspaceInit[AutoSandboxGitBranchInput] = normalized

	merged := make(map[string]interface{}, len(toolInit))
	for key, value := range toolInit {
		merged[key] = value
	}
	merged[AutoSandboxToolInitKey] = workspaceInit
	return merged, true
}

// WithAutoSandboxBranch returns config whose automatic per-chat sandbox checks
// out branch. It is the config-level convenience over BindAutoSandboxBranch; a
// config without a tool_init (or without an auto_sandbox workspace init) is
// returned unchanged.
func WithAutoSandboxBranch(config map[string]interface{}, branch string) map[string]interface{} {
	if config == nil {
		return config
	}
	toolInit, ok := config["tool_init"].(map[string]interface{})
	if !ok {
		return config
	}
	bound, changed := BindAutoSandboxBranch(toolInit, branch)
	if !changed {
		return config
	}
	merged := make(map[string]interface{}, len(config))
	for key, value := range config {
		merged[key] = value
	}
	merged["tool_init"] = bound
	return merged
}

// autoSandboxEnabled reports whether the workspace init asked for the automatic
// per-chat sandbox. Configs come from JSON/YAML, so both a real bool and its
// string form are accepted.
func autoSandboxEnabled(workspaceInit map[string]interface{}) bool {
	switch value := workspaceInit[AutoSandboxFlagInput].(type) {
	case bool:
		return value
	case string:
		normalized := strings.ToLower(strings.TrimSpace(value))
		return normalized == "true" || normalized == "1" || normalized == "yes"
	default:
		return false
	}
}

// BindAutoSandboxRepo returns tool_init whose automatic per-chat sandbox clones
// repo. changed reports whether anything was altered and the returned map is the
// caller's map when it was not, so a caller can cheaply keep the previous value.
//
// repo replaces a previously configured (bot-default) git_repo: the caller is
// responsible for not calling this when the user pinned the sandbox repository
// explicitly (see ExplicitAutoSandboxRepo). It is otherwise conservative and
// side-effect free:
//   - tool_init without a coder_select_workspace entry (ie any non-coder bot, or
//     an integration that does not take the sandbox init) is untouched,
//   - a coder_select_workspace entry that does not switch auto_sandbox on keeps
//     exactly its configured inputs, so a fixed/pinned workspace is never mixed
//     with a clone target,
//   - a value that names no usable remote leaves the default checkout in place,
//   - every other tool_init input is preserved and the caller's maps are copied,
//     never mutated.
func BindAutoSandboxRepo(toolInit map[string]interface{}, repo string) (result map[string]interface{}, changed bool) {
	remote := GitRemoteForRepo(repo)
	if remote == "" || toolInit == nil {
		return toolInit, false
	}
	existing, ok := toolInit[AutoSandboxToolInitKey].(map[string]interface{})
	if !ok || !autoSandboxEnabled(existing) {
		return toolInit, false
	}
	if current, ok := existing[AutoSandboxGitRepoInput].(string); ok && strings.TrimSpace(current) == remote {
		return toolInit, false
	}

	workspaceInit := make(map[string]interface{}, len(existing)+1)
	for key, value := range existing {
		workspaceInit[key] = value
	}
	workspaceInit[AutoSandboxGitRepoInput] = remote

	merged := make(map[string]interface{}, len(toolInit))
	for key, value := range toolInit {
		merged[key] = value
	}
	merged[AutoSandboxToolInitKey] = workspaceInit
	return merged, true
}

// WithAutoSandboxRepo returns config whose automatic per-chat sandbox clones
// repo. It is the config-level convenience over BindAutoSandboxRepo; a config
// without a tool_init (or without an auto_sandbox workspace init) is returned
// unchanged.
func WithAutoSandboxRepo(config map[string]interface{}, repo string) map[string]interface{} {
	if config == nil {
		return config
	}
	toolInit, ok := config["tool_init"].(map[string]interface{})
	if !ok {
		return config
	}
	bound, changed := BindAutoSandboxRepo(toolInit, repo)
	if !changed {
		return config
	}
	merged := make(map[string]interface{}, len(config))
	for key, value := range config {
		merged[key] = value
	}
	merged["tool_init"] = bound
	return merged
}
