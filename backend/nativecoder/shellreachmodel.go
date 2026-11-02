package nativecoder

// Shell-tool reach model for the opencode parity harness (ci#394 gap
// "shell_modes"). opencode's bash tool is *one* call: the integration runs the
// process, streams its output, and answers stdin/keyboard input while it runs.
// The native coder_bash call is the same single tool, so the harness cannot
// decide "reach" from the tool name alone — it has to look at the arguments of
// each call. This file is that job.
//
//   - CoderShellCallKind models what a coder_bash call needs (run the command;
//     keep observing a still-running command; answer a prompt), so the harness
//     can report that a run *used* live shell handling rather than that a tool
//     was merely listed in the manifest.
//   - CoderShellShowTellDetector is the parse/battle-test hook for whatever
//     adapter the runtime wires in later.
//
// The file holds no process state: the session store (CoderShellStore) is
// already the single source of truth for running/streaming/output. This model
// only classifies calls so a run can prove it reached the live modes.

import (
	"errors"
	"strings"
)

// CoderShellCallKind is one thing a coder_bash call needed from the tool loop.
type CoderShellCallKind string

const (
	// CoderShellCallRun is a single-shot request that must be executed to
	// completion (like the classic foreground coder_bash call).
	CoderShellCallRun CoderShellCallKind = "run"
	// CoderShellCallLive is a request that must keep running after its first
	// response (a dev server, watcher or `tail -f`), which only a background
	// or streaming session can satisfy.
	CoderShellCallLive CoderShellCallKind = "live"
	// CoderShellCallInteract is a request that must also accept stdin while it
	// runs (a REPL or a prompt-driven command), which only an interactive
	// session can satisfy.
	CoderShellCallInteract CoderShellCallKind = "interact"
)

// coderShellKindOfVerb maps a leading verb of a command to what the call needs.
// The map is a deliberate, small heuristic, not a shell parser: a command the
// mapper does not know is a `run` (single-shot), and the harness only uses the
// verdict to shape its evidence — the confirmation layer always decides what a
// call may do. Keeping the vocabulary tight makes the model auditable.
var coderShellKindOfVerb = map[string]CoderShellCallKind{
	"dev":     CoderShellCallLive,
	"serve":   CoderShellCallLive,
	"server":  CoderShellCallLive,
	"start":   CoderShellCallLive,
	"watch":   CoderShellCallLive,
	"tail":    CoderShellCallLive,
	"wait":    CoderShellCallLive,
	"repl":    CoderShellCallInteract,
	"ipython": CoderShellCallInteract,
	"node":    CoderShellCallInteract,
	"python":  CoderShellCallInteract,
	"pytest":  CoderShellCallInteract,
}

// CoderShellLiveVerbs returns the verbs that classify a call as needing a live
// session, and CoderShellInteractiveVerbs the ones that need stdin. They exist
// so an adapter and the harness agree on which commands "count" without both
// importing an unexported map.
func CoderShellLiveVerbs() []string {
	return []string{"dev", "serve", "server", "start", "watch", "tail", "wait"}
}

func CoderShellInteractiveVerbs() []string {
	return []string{"repl", "ipython", "node", "python", "pytest"}
}

// CoderShellShortVerb returns the first word of a command after `cd A && ...`
// is skipped, lowercased and trimmed. A command that is only whitespace returns
// "". It does not try to resolve `sh -c '...'` wrappers: an adapter wrapping
// such a command can normalize it first.
func CoderShellShortVerb(command string) string {
	tokens := coderShellTokens(command)
	if len(tokens) == 0 {
		return ""
	}
	return strings.ToLower(tokens[0])
}

// coderShellTokens splits a command into whitespace-separated tokens after
// `cd A && ...` prefixes are hoisted, so `npm run dev` and `cd a && npm run
// dev` are classified identically. Quoting is not interpreted: the kind of a
// call is decided from its leading vocabulary, not from a full shell parse.
func coderShellTokens(command string) []string {
	command = strings.TrimSpace(command)
	for strings.HasPrefix(command, "cd ") && strings.Contains(command, "&&") {
		command = strings.TrimSpace(command[strings.Index(command, "&&")+2:])
	}
	return strings.Fields(command)
}

// CoderShellRunnerVerbs are wrapper commands whose *next* word is the real
// verb (`npm run dev`, `cargo dev`, `go test`, `make watch`). Classifying the
// wrapper alone would mislabel a dev server as a single-shot run.
var coderShellRunnerVerbs = map[string]bool{
	"npm":   true,
	"pnpm":  true,
	"yarn":  true,
	"bun":   true,
	"cargo": true,
	"go":    true,
	"make":  true,
}

// CoderShellKindForCommand classifies a coder_bash command string.
func CoderShellKindForCommand(command string) CoderShellCallKind {
	tokens := coderShellTokens(command)
	if len(tokens) == 0 {
		return CoderShellCallRun
	}
	verb := strings.ToLower(tokens[0])
	if kind, ok := coderShellKindOfVerb[verb]; ok {
		return kind
	}
	if coderShellRunnerVerbs[verb] && len(tokens) >= 2 {
		if kind, ok := coderShellKindOfVerb[strings.ToLower(tokens[1])]; ok {
			return kind
		}
		if strings.ToLower(tokens[1]) == "run" && len(tokens) >= 3 {
			if kind, ok := coderShellKindOfVerb[strings.ToLower(tokens[2])]; ok {
				return kind
			}
		}
	}
	return CoderShellCallRun
}

// CoderShellDetector is the hook a shell adapter is expected to implement so
// the parity harness can prove a run reached live/streaming commands. It is
// kept intentionally small: detect what a call needs, and let the harness
// decide what that proves.
type CoderShellDetector interface {
	// DetectKind reports what one coder_bash call needs from the tool loop.
	DetectKind(command string) CoderShellCallKind
}

// CoderShellShowTellDetector returns the show-and-tell detector used by the
// parity harness: it demonstrates the runtime exercised a live/interactive
// call, without being the place the confirmation decision is made.
func CoderShellShowTellDetector() CoderShellDetector {
	return coderShellShowTellDetector{}
}

type coderShellShowTellDetector struct{}

func (coderShellShowTellDetector) DetectKind(command string) CoderShellCallKind {
	return CoderShellKindForCommand(command)
}

// ErrCoderShellDetectCommand guards the detector against empty input.
var ErrCoderShellDetectCommand = errors.New("coder shell: cannot detect a kind for an empty command")

// CoderShellDetectKind is the package-level helper for one-off detection (used
// by tests and small adapters). An empty command is an error, mirroring the
// store's rejection of an empty command.
func CoderShellDetectKind(command string) (CoderShellCallKind, error) {
	if strings.TrimSpace(command) == "" {
		return "", ErrCoderShellDetectCommand
	}
	return CoderShellKindForCommand(command), nil
}
