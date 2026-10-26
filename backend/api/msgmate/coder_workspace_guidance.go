package msgmate

import (
	"strings"

	"backend/nativecoder"
)

// workspaceRootPromptMarker is the leading token of
// nativecoder.WorkspaceRootGuidance(), used to keep the append idempotent when a
// caller has already composed the guidance into the configured prompt.
const workspaceRootPromptMarker = "Workspace root:"

// coderWorkspaceSystemPrompt appends the ci#819 part A workspace-root guidance to
// a coder's system prompt, so the model learns the sandbox root up front instead
// of rediscovering it with a `cd` prefix on every `coder_bash` call.
//
// It is deliberately generic: the root comes from the sandbox abstraction
// (nativecoder.WorkspaceRootDefault), never from repository knowledge, and a
// config that is not a native coder agent is returned untouched. That keeps the
// shared handler path behaviour-neutral for every non-coder bot.
func coderWorkspaceSystemPrompt(systemPrompt string, config map[string]interface{}) string {
	if !nativecoder.IsCoderAgentConfig(config) {
		return systemPrompt
	}
	// Name the root the session layer will actually apply: a sandbox that
	// declares coder_workspace_root must not be told the default path.
	guidance := strings.TrimSpace(nativecoder.WorkspaceRootGuidanceFor(nativecoder.WorkspaceRootFromConfig(config)))
	// ci#514 phase 1: the prompt half of retiring coder_bash for GitHub work.
	// The block names the typed GitHub tool for every recurring `gh` workflow,
	// so the model reaches for a typed, auditable call first. It is idempotent
	// and only composed for a native coder agent.
	guidance = nativecoder.GitHubTypedFirstAppend(guidance)
	// ci#514 phase 3: the prompt half of retiring coder_bash for git work. The
	// block names the typed coder_git_* reads/writes so no dev-agent prompt
	// instructs a raw `git` command any more; it is idempotent.
	guidance = nativecoder.GitTypedFirstAppend(guidance)
	if guidance == "" {
		return systemPrompt
	}
	trimmed := strings.TrimSpace(systemPrompt)
	if strings.Contains(trimmed, workspaceRootPromptMarker) {
		// The workspace-root half is already composed (a config that pre-baked
		// the guidance): never emit it twice, the prompt is paid for on every
		// turn. The typed-first halves are appended on their own here, because
		// a pre-baked prompt must not be the one case that stays typed-blind.
		return nativecoder.GitTypedFirstAppend(nativecoder.GitHubTypedFirstAppend(systemPrompt))
	}
	if trimmed == "" {
		return guidance
	}
	return trimmed + "\n\n" + guidance
}
