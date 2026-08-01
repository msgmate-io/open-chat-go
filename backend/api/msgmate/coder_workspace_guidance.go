package msgmate

import (
	"bytes"
	"encoding/json"
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
	if guidance == "" {
		return systemPrompt
	}
	trimmed := strings.TrimSpace(systemPrompt)
	if strings.Contains(trimmed, workspaceRootPromptMarker) {
		// Already composed (a config that pre-baked the guidance): never emit it
		// twice, the prompt is paid for on every turn.
		return systemPrompt
	}
	if trimmed == "" {
		return guidance
	}
	return trimmed + "\n\n" + guidance
}

// coderBashToolName is the single shell tool whose leading root navigation the
// tolerance half rewrites. Every other tool is left alone.
const coderBashToolName = "coder_bash"

// coderBashCommandArg is the coder_bash argument key carrying the shell command.
const coderBashCommandArg = "command"

// normalizeCoderBashArguments strips a redundant leading `cd <workspace root>`
// (or `cd $(git rev-parse --show-toplevel)`) from a `coder_bash` command. It is
// the tolerance half of ci#819 part A: the system prompt asks the model not to
// write the prefix, and this removes it when an older habit still does, so the
// call is not wasted on a no-op.
//
// The rewrite is deliberately narrow: only the `coder_bash` tool, only its
// `command` argument, and only a FIRST segment that navigates to the workspace
// root and is followed by real work. A subdirectory descent, a `..`, a command
// without the prefix, a navigation-only command (which would dispatch an empty
// shell line) and any unparseable arguments are returned byte-identical, so the
// shared dispatch path stays behaviour-neutral for everything else.
//
// This runs in the tool loop, which does not carry the shared config, so a
// declared coder_workspace_root cannot be named here and the default root is
// assumed. The `cd $(git rev-parse --show-toplevel)` spelling is root-independent
// and is therefore still caught for a non-default sandbox; only a literal `cd
// <declared root>` prefix survives, which the prompt half (which does read the
// config) already asks the model not to write.
func normalizeCoderBashArguments(name, arguments string) string {
	if name != coderBashToolName || arguments == "" {
		return arguments
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil || parsed == nil {
		return arguments
	}
	command, ok := parsed[coderBashCommandArg].(string)
	if !ok || command == "" {
		return arguments
	}
	stripped, changed := nativecoder.StripRedundantWorkspaceRootCD(command, nativecoder.WorkspaceRootDefault)
	if !changed || stripped == "" {
		return arguments
	}
	parsed[coderBashCommandArg] = stripped
	// A plain json.Marshal would HTML-escape `&&` / `>` into `\u0026` / `\u003e`,
	// which decodes identically but leaves the transcript and the stored
	// signature unreadable, so encode without HTML escaping.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(parsed); err != nil {
		return arguments
	}
	normalized := strings.TrimRight(buf.String(), "\n")
	if strings.TrimSpace(normalized) == "" {
		return arguments
	}
	return normalized
}
