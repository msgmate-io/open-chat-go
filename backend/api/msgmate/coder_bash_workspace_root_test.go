package msgmate

import (
	"encoding/json"
	"strings"
	"testing"

	"backend/nativecoder"
)

// coderBashCommand extracts the `command` of normalized coder_bash arguments.
func coderBashCommand(t *testing.T, arguments string) string {
	t.Helper()
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		t.Fatalf("normalized arguments are not valid JSON: %v (%q)", err, arguments)
	}
	command, _ := parsed["command"].(string)
	return command
}

// TestNormalizeCoderBashArgumentsStripsRootPrefix pins the tolerance half of
// ci#819 part A for the literal root spelling.
func TestNormalizeCoderBashArgumentsStripsRootPrefix(t *testing.T) {
	args := `{"command":"cd ` + nativecoder.WorkspaceRootDefault + ` && go test ./backend/nativecoder/..."}`

	got := normalizeCoderBashArguments(coderBashToolName, args)

	if command := coderBashCommand(t, got); command != "go test ./backend/nativecoder/..." {
		t.Fatalf("got command %q, want the prefix removed", command)
	}
}

// TestNormalizeCoderBashArgumentsStripsGitRevParsePrefix covers the spelling the
// guidance exists to retire, which is the one agents reach for instead of the
// literal path.
func TestNormalizeCoderBashArgumentsStripsGitRevParsePrefix(t *testing.T) {
	args := `{"command":"cd $(git rev-parse --show-toplevel) && git status --short"}`

	got := normalizeCoderBashArguments(coderBashToolName, args)

	if command := coderBashCommand(t, got); command != "git status --short" {
		t.Fatalf("got command %q, want the git-rev-parse prefix removed", command)
	}
}

// TestNormalizeCoderBashArgumentsKeepsRealNavigation guards the rewrite's
// boundary: a descent into a subdirectory is real navigation, not the redundant
// root prefix, so it must survive untouched.
func TestNormalizeCoderBashArgumentsKeepsRealNavigation(t *testing.T) {
	for _, command := range []string{
		"cd backend/nativecoder && go test ./...",
		"cd .. && ls",
		"go test ./backend/nativecoder/...",
		"grep -rn 'cd " + nativecoder.WorkspaceRootDefault + "' .",
	} {
		args, err := json.Marshal(map[string]interface{}{"command": command})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if got := normalizeCoderBashArguments(coderBashToolName, string(args)); got != string(args) {
			t.Fatalf("command %q must not be rewritten, got %q", command, got)
		}
	}
}

// TestNormalizeCoderBashArgumentsIgnoresOtherTools keeps the rewrite off every
// non-shell tool, so `coder_read`/`coder_edit` payloads (a path, a file body)
// are never touched even when they contain the prefix text.
func TestNormalizeCoderBashArgumentsIgnoresOtherTools(t *testing.T) {
	args := `{"command":"cd ` + nativecoder.WorkspaceRootDefault + ` && go test ./..."}`

	for _, name := range []string{"coder_read", "coder_edit", "coder_git", "cronjob"} {
		if got := normalizeCoderBashArguments(name, args); got != args {
			t.Fatalf("tool %q must be untouched, got %q", name, got)
		}
	}
}

// TestNormalizeCoderBashArgumentsPassesThroughMalformed keeps a dispatch error
// reportable: arguments that are not JSON, carry no command, or navigate and do
// nothing else are returned byte-identical rather than replaced by an empty shell
// line.
func TestNormalizeCoderBashArgumentsPassesThroughMalformed(t *testing.T) {
	for _, args := range []string{
		"",
		"not json",
		`{}`,
		`{"command":""}`,
		`{"command":42}`,
		`{"command":"cd ` + nativecoder.WorkspaceRootDefault + `"}`,
		`{"command":"cd ` + nativecoder.WorkspaceRootDefault + ` && "}`,
	} {
		if got := normalizeCoderBashArguments(coderBashToolName, args); got != args {
			t.Fatalf("arguments %q must be untouched, got %q", args, got)
		}
	}
}

// TestNormalizeCoderBashArgumentsKeepsShellOperatorsReadable guards the stored
// form: `&&` must survive as `&&`, not as a `\u0026\u0026` escape, or the transcript and
// the duplicate-call signature become unreadable.
func TestNormalizeCoderBashArgumentsKeepsShellOperatorsReadable(t *testing.T) {
	args := `{"command":"cd $(git rev-parse --show-toplevel) && go test ./... 2>&1 | tail -5 && echo done"}`

	got := normalizeCoderBashArguments(coderBashToolName, args)

	if strings.Contains(got, `\u0026`) || strings.Contains(got, `\u003e`) || strings.Contains(got, `\u003c`) {
		t.Fatalf("normalized arguments must not HTML-escape shell operators: %q", got)
	}
	if !strings.Contains(got, "&&") || !strings.Contains(got, "2>&1") {
		t.Fatalf("shell operators lost from %q", got)
	}
}

// TestNormalizeCoderBashArgumentsPreservesSiblingArguments checks that the
// rewrite edits only `command` and keeps an accompanying field, so a reason
// (ci#723) or timeout survives the normalization.
func TestNormalizeCoderBashArgumentsPreservesSiblingArguments(t *testing.T) {
	args := `{"command":"cd $(git rev-parse --show-toplevel) && make test","reason":"run the suite"}`

	got := normalizeCoderBashArguments(coderBashToolName, args)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("normalized arguments are not valid JSON: %v", err)
	}
	if parsed["reason"] != "run the suite" {
		t.Fatalf("sibling argument lost: %q", got)
	}
	if parsed["command"] != "make test" {
		t.Fatalf("got command %v, want the prefix removed", parsed["command"])
	}
}
