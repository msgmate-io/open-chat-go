package msgmate

import "strings"

// Duplicate-call framing (ci#728 item 3).
//
// An exact repeat of a call that already succeeded in the same turn is served
// from the per-turn result cache (`executedToolResults` in openai.go) instead of
// being dispatched again. Returning the cached payload *verbatim* was a trap:
// the model could not tell a replayed result from a fresh one, so it re-issued
// the identical call again and again until the turn was stopped (ci#569: 110
// tool calls, 18 identical `coder_read`). A replayed result now carries an
// explicit frame that says the call already ran in this turn, that it must not
// be re-issued, and what to do instead (act on the result, or narrow the
// arguments).
const (
	// duplicateToolCallNoticePrefix opens the frame. It is kept as one line so
	// a UI row does not grow a wall of text.
	duplicateToolCallNoticePrefix = "[duplicate tool call] "

	// duplicateToolCallNoticeSuffix closes the frame, after the replayed
	// payload, so the instruction is the last thing the model reads.
	duplicateToolCallNoticeSuffix = "\n[duplicate tool call: the call above was NOT executed again - its state may be stale. " +
		"Do NOT re-issue it: act on the result you already have, or change the arguments." +
		"%s]"

	// duplicateToolCallNarrowHint is the tool-specific "what to do instead"
	// hint appended to the frame.
	duplicateToolCallNarrowHint = " For %s, narrow the range instead (" +
		"`coder_read` with `offset`/`limit`, `coder_list` with `depth`, " +
		"`coder_grep` with `path`/`include`, `coder_bash` redirecting to a file)."
)

// duplicateToolCallHints maps a tool name to the concrete alternative to a
// repeated call, so the frame is actionable instead of a generic refusal.
var duplicateToolCallHints = map[string]string{
	"coder_read": " For coder_read, read a narrower `offset`/`limit` range or a different file.",
	"coder_list": " For coder_list, list a shallower `depth` or a subdirectory instead of the whole tree.",
	"coder_grep": " For coder_grep, narrow the pattern and pass an explicit `path`/`include`.",
	"coder_bash": " For coder_bash, run a narrower command or redirect the output to a file and read the ranges you need.",
	"coder_git":  " For coder_git, ask for a smaller range (`limit`, a single `path`, or one commit).",
	"coder_glob": " For coder_glob, use a tighter pattern so the match set is smaller.",
}

// DuplicateToolCallNotice returns the one-line notice that opens a replayed
// tool result and names the tool that was not actually re-executed.
func DuplicateToolCallNotice(toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "this tool call"
	}
	return duplicateToolCallNoticePrefix + name + " was already executed in this turn with exactly these arguments."
}

// DuplicateToolCallNextStep returns the tool-specific instruction telling the
// model what to do instead of re-issuing the identical call.
func DuplicateToolCallNextStep(toolName string) string {
	name := strings.TrimSpace(toolName)
	if hint, ok := duplicateToolCallHints[name]; ok {
		return hint
	}
	return ""
}

// FrameDuplicateToolCallResult wraps a replayed cached result in the explicit
// duplicate-call frame (ci#728), so the model sees that the call already ran
// and was not re-executed. The replayed payload itself is preserved verbatim
// between the two markers - the model may still need its content.
func FrameDuplicateToolCallResult(toolName, cachedResult string) string {
	nextStep := ""
	if hint := DuplicateToolCallNextStep(toolName); hint != "" {
		nextStep = hint
	} else if strings.TrimSpace(toolName) != "" {
		nextStep = strings.Replace(duplicateToolCallNarrowHint, "%s", strings.TrimSpace(toolName), 1)
	}
	header := DuplicateToolCallNotice(toolName) + "\n"
	if strings.TrimSpace(cachedResult) == "" {
		cachedResult = "(the call returned no output)"
	}
	return header + cachedResult + strings.Replace(duplicateToolCallNoticeSuffix, "%s", nextStep, 1)
}
