package nativecoder

import (
	"strings"

	"backend/sandboxsession"
)

// ci#819: the sandbox-agnostic "workspace root default".
//
// Every native coder interaction runs in a disposable per-chat sandbox that
// clones the repository into one directory (ci#413). Which directory that is
// used to be a deployment detail the agent had to *discover*: commands were
// written as `cd /workspace && …` (or the equally redundant
// `cd $(git rev-parse --show-toplevel) && …`) in nearly every `coder_bash`
// call, which cost a turn's worth of tokens, moved the shell out of the tools'
// own root resolution, and broke the moment a sandbox used a root other than
// `/workspace`.
//
// The fix is an abstraction, not a literal: the shell/session layer resolves
// the workspace root itself (sandboxsession.ResolveWorkDir, defaulting to
// sandboxsession.DefaultWorkDir) and hands it to the model up front, so the
// agent works workspace-relative and only ever changes directory to descend
// into a subdirectory. This file is the native-coder half: the constant the
// guidance is rendered from, the prompt lines that state the root up front, the
// shared-config key a sandbox can use to declare a non-standard root, and the
// helper that recognises (and strips) a redundant root `cd` when a caller still
// writes one.
//
// No secrets, tokens or deployment configuration are part of this data: it is
// one path, a config key and one-line guidance only.

// WorkspaceRootDefault is the sandbox path a workspace is cloned into unless the
// sandbox declares another one. It is the same default the shell/session layer
// applies (sandboxsession.DefaultWorkDir), so the guidance the model reads and
// the working directory it actually gets can never drift apart.
const WorkspaceRootDefault = sandboxsession.DefaultWorkDir

// WorkspaceRootConfigKey is the optional shared-config key that declares the
// workspace root of a non-standard sandbox. Absent (or empty) means
// WorkspaceRootDefault; the value is a container path, never a host path and
// never a credential.
const WorkspaceRootConfigKey = "coder_workspace_root"

// WorkspaceRootGuidanceLines returns the model-facing prompt lines that state
// the workspace root up front, so a coder does not have to discover it (and
// stops prefixing every command with a `cd` into it).
func WorkspaceRootGuidanceLines() []string {
	root := WorkspaceRootDefault
	return []string{
		"Workspace root: `" + root + "` is the root of your checkout. Every `coder_bash` command already " +
			"starts there, and a workspace-relative path in `coder_read` / `coder_list` / `coder_glob` / " +
			"`coder_grep` resolves against it.",
		"Do not prefix a command with `cd " + root + "` or `cd $(git rev-parse --show-toplevel)`: it is a " +
			"no-op that wastes a turn. Pass the workspace-relative path (eg `go test ./backend/nativecoder/...`) " +
			"and use `cd <subdir> && ...` only to descend into a subdirectory.",
	}
}

// WorkspaceRootGuidance returns WorkspaceRootGuidanceLines as one paragraph,
// which is the shape a single prompt block wants.
func WorkspaceRootGuidance() string {
	return strings.Join(WorkspaceRootGuidanceLines(), " ")
}

// WorkspaceRootFromConfig reads the declared workspace root of a sandbox config,
// falling back to WorkspaceRootDefault when the key is absent, empty or not a
// string. The result is trimmed so a value pasted with whitespace still matches
// a rendered `cd` target.
func WorkspaceRootFromConfig(config map[string]interface{}) string {
	if config != nil {
		if raw, ok := config[WorkspaceRootConfigKey].(string); ok {
			if trimmed := strings.TrimSpace(raw); trimmed != "" {
				return sandboxsession.ResolveWorkDir(trimmed)
			}
		}
	}
	return WorkspaceRootDefault
}

// WithWorkspaceRoot returns config with the workspace root declared. An empty
// root is a no-op (the default stands) and the caller's map is copied, never
// mutated.
func WithWorkspaceRoot(config map[string]interface{}, root string) map[string]interface{} {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return config
	}
	if config == nil {
		config = map[string]interface{}{}
	}
	if existing, ok := config[WorkspaceRootConfigKey].(string); ok && strings.TrimSpace(existing) == trimmed {
		return config
	}
	merged := make(map[string]interface{}, len(config)+1)
	for key, value := range config {
		merged[key] = value
	}
	merged[WorkspaceRootConfigKey] = trimmed
	return merged
}

// WorkspaceRootCDTargets returns the literal `cd` targets that are equivalent to
// the workspace root: the plain root, its trailing-slash spelling, the
// single/double-quoted forms, and the `$(git rev-parse --show-toplevel)` spelling
// coders paste instead of the literal path.
func WorkspaceRootCDTargets(root string) []string {
	clean := strings.TrimRight(strings.TrimSpace(root), "/")
	if clean == "" {
		clean = "/"
	}
	targets := []string{clean, `"` + clean + `"`, "'" + clean + "'", "$(git rev-parse --show-toplevel)"}
	if clean != "/" {
		targets = append(targets,
			clean+"/",
			`"`+clean+`/"`,
			"'"+clean+"/'",
			"$(git rev-parse --show-toplevel)/",
		)
	}
	return targets
}

// StripRedundantWorkspaceRootCD removes a leading `cd <workspace root>`
// navigation from a shell command. It is the tolerance half of the abstraction:
// the guidance asks the agent not to write the prefix, and this strips it when
// an older habit (or a cached prompt) still does.
//
// The command is only rewritten when its FIRST segment navigates to the
// workspace root and is followed by more work; anything else - a real `cd
// backend`, a `cd ..`, a command without a leading `cd` - is returned unchanged,
// as is a command that is nothing but the redundant `cd` itself (changed
// reports the no-op so a caller can drop it instead of running an empty line).
// changed reports whether the returned command differs from the input.
func StripRedundantWorkspaceRootCD(command, root string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return command, false
	}
	targets := WorkspaceRootCDTargets(root)
	for _, target := range targets {
		prefix := "cd " + target
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		rest := strings.TrimSpace(trimmed[len(prefix):])
		// Nothing but the navigation: the caller should drop the call.
		if rest == "" {
			return "", true
		}
		for _, separator := range []string{"&&", ";"} {
			if strings.HasPrefix(rest, separator) {
				if next := strings.TrimSpace(rest[len(separator):]); next != "" {
					return next, true
				}
			}
		}
	}
	return command, false
}
