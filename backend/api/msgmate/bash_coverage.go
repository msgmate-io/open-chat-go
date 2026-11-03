package msgmate

import (
	"encoding/json"
	"sort"
	"strings"
)

// Bash command coverage classification (ci#514, step S1 "measure").
//
// This file is the measurement half of the `coder_bash` retirement plan: it
// classifies a shell command line into the recurring buckets observed in the
// historical coder transcripts (GitHub CLI work, repo inspection, build/test/run,
// scaffolding, environment probing and the integration CLIs) so a later phase can
// report how much of the real `coder_bash` traffic the typed tool surface already
// covers - without changing any behaviour.
//
// The classifier is deliberately pure and dependency-free (no shell execution, no
// sandbox state): it only reads the command string, so it is cheap enough to run
// on every `coder_bash` call and deterministic enough to unit test.

// BashCategory is the bucket of one shell command line.
type BashCategory string

const (
	// BashCategoryGitHub is `gh …`: issue/PR/release/run work.
	BashCategoryGitHub BashCategory = "github"
	// BashCategoryRepoInspection is reading the workspace: ls/find/cat/grep/…
	BashCategoryRepoInspection BashCategory = "repo_inspection"
	// BashCategoryBuildTestRun is building, testing, linting or running a project.
	BashCategoryBuildTestRun BashCategory = "build_test_run"
	// BashCategoryScaffold is creating project structure: mkdir/touch/init/…
	BashCategoryScaffold BashCategory = "scaffold"
	// BashCategoryEnvProbe is inspecting the environment: which/env/uname/…
	BashCategoryEnvProbe BashCategory = "env_probe"
	// BashCategoryIntegrationCLI is the openchat/integration manager plus the
	// container/orchestration CLIs it drives.
	BashCategoryIntegrationCLI BashCategory = "integration_cli"
	// BashCategoryOther is everything the typed surface is not expected to cover
	// yet: arbitrary pipelines, installers, REPLs, one-off scripting.
	BashCategoryOther BashCategory = "other"
)

// BashCategories lists every category in registry order (used for stable reports).
var BashCategories = []BashCategory{
	BashCategoryGitHub,
	BashCategoryRepoInspection,
	BashCategoryBuildTestRun,
	BashCategoryScaffold,
	BashCategoryEnvProbe,
	BashCategoryIntegrationCLI,
	BashCategoryOther,
}

// bashCategoryCommands maps a category to the leading command words that select
// it. The first match of the first non-wrapper segment wins.
var bashCategoryCommands = map[BashCategory][]string{
	BashCategoryGitHub:         {"gh"},
	BashCategoryIntegrationCLI: {"openchat", "openchat-integrations", "oc", "msgmate", "kubectl", "k9s", "docker", "docker-compose", "helm", "kustomize", "systemctl", "journalctl", "ctr", "nerdctl"},
	BashCategoryBuildTestRun:   {"go", "gofmt", "goimports", "gofumpt", "govulncheck", "gotestsum", "make", "npm", "npx", "yarn", "pnpm", "bun", "cargo", "rustc", "pytest", "tox", "vitest", "jest", "mypy", "ruff", "flake8", "golangci-lint", "staticcheck", "tsc", "eslint", "prettier", "mvn", "gradle", "bazel", "cmake", "gcc", "clang", "bash", "sh", "zsh", "shellcheck"},
	BashCategoryRepoInspection: {"ls", "find", "tree", "cat", "bat", "head", "tail", "less", "more", "wc", "grep", "rg", "ag", "sed", "awk", "cut", "sort", "uniq", "diff", "comm", "file", "stat", "readlink", "realpath", "pwd", "du", "column", "jq", "yq", "xxd", "hexdump", "strings", "od", "md5sum", "sha256sum", "git"},
	BashCategoryScaffold:       {"mkdir", "rmdir", "touch", "cp", "ln", "tee", "install", "rsync", "curl", "wget", "unzip", "tar", "zip", "gzip", "gunzip", "patch", "truncate", "mktemp", "cookiecutter"},
	BashCategoryEnvProbe:       {"which", "type", "env", "printenv", "uname", "whoami", "id", "hostname", "date", "df", "free", "ps", "top", "uptime", "lsof", "ss", "netstat", "dig", "nslookup", "ping", "nproc", "getconf", "ulimit", "locale"},
}

// bashInterpreterCommands are language runtimes whose category depends on how they
// are invoked: a script or `-m module` is project build/test/run work, while a bare
// REPL or a `-c`/`-e` one-liner is arbitrary scripting (the guarded hatch).
var bashInterpreterCommands = map[string]bool{
	"python": true, "python3": true, "node": true, "deno": true, "bun": true,
	"ruby": true, "perl": true, "php": true, "lua": true, "java": true,
	"dotnet": true, "swift": true, "Rscript": true,
}

// bashScriptExtensions marks a non-flag argument as a script to execute.
var bashScriptExtensions = []string{".py", ".js", ".mjs", ".cjs", ".ts", ".rb", ".pl", ".php", ".lua", ".sh", ".bash", ".jar", ".swift"}

// bashWrapperCommands are transparent prefixes: they do not decide the category by
// themselves, so classification continues with the remaining words.
var bashWrapperCommands = map[string]bool{
	"sudo": true, "doas": true, "time": true, "command": true, "exec": true,
	"nohup": true, "builtin": true, "cd": true, "set": true, "export": true,
	"source": true, "eval": true, "echo": true, "printf": true, "true": true,
}

// BashCategoryTypedTools names the typed tool family that replaces a category, so
// the steering hints (ci#514 S2) and the coverage report agree on one mapping. A
// category without an entry is not (yet) covered by typed tools.
var BashCategoryTypedTools = map[BashCategory][]string{
	BashCategoryGitHub:         {"github_issue_list", "github_issue_view", "github_pr_view", "github_pr_create", "github_pr_comment", "github_run_list", "github_run_view", "github_file_read", "github_auth_status"},
	BashCategoryRepoInspection: {"coder_read", "coder_list", "coder_glob", "coder_grep"},
	BashCategoryBuildTestRun:   {"coder_run", "coder_install"},
	BashCategoryScaffold:       {"coder_write", "coder_edit", "coder_install"},
	BashCategoryEnvProbe:       {"coder_env"},
	BashCategoryIntegrationCLI: {"kubernetes_describe", "docker_sandbox_status", "coder_env"},
}

// ClassifyBashCommand classifies one shell command line into its dominant bucket.
//
// The line is split on the common command separators (`&&`, `||`, `;`, `|`, `&`,
// newline) and each segment is classified independently; the first segment whose
// category is not `other` decides, so `cd /x && go test ./...` is a build/test/run
// command and an unruly pipeline stays `other`. An empty command is `other`.
func ClassifyBashCommand(command string) BashCategory {
	for _, segment := range splitBashSegments(command) {
		if category, ok := classifyBashSegment(segment); ok {
			return category
		}
	}
	return BashCategoryOther
}

// splitBashSegments splits a command line into the individual commands it runs.
func splitBashSegments(command string) []string {
	replaced := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n", "\n", "\n").Replace(command)
	segments := []string{}
	for _, segment := range strings.Split(replaced, "\n") {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			segments = append(segments, trimmed)
		}
	}
	return segments
}

// classifyBashSegment classifies a single (non-compound) segment. Wrapper prefixes
// (`sudo`, `cd`, `VAR=value`, redirections) are skipped; the first remaining word
// is matched against the command table.
func classifyBashSegment(segment string) (BashCategory, bool) {
	words := strings.Fields(segment)
	for len(words) > 0 {
		word := words[0]
		// Strip a leading env assignment (`FOO=bar cmd`) or a redirection.
		if strings.Contains(word, "=") && !strings.HasPrefix(word, "-") || strings.HasPrefix(word, "<") || strings.HasPrefix(word, ">") {
			words = words[1:]
			continue
		}
		// Strip a path prefix and a leading `-c` style shell argument.
		base := word
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		if strings.HasPrefix(base, "-") {
			words = words[1:]
			continue
		}
		if bashWrapperCommands[base] {
			words = words[1:]
			continue
		}
		commandWord := base
		if bashInterpreterCommands[commandWord] {
			if bashInterpreterRunsProjectWork(words[1:]) {
				return BashCategoryBuildTestRun, true
			}
			// A bare REPL, `-c` one-liner or `-e` eval is arbitrary scripting.
			return BashCategoryOther, false
		}
		for cat, commandWords := range bashCategoryCommands {
			for _, candidate := range commandWords {
				if commandWord == candidate {
					return cat, true
				}
			}
		}
		return BashCategoryOther, false
	}
	return BashCategoryOther, false
}

// bashInterpreterRunsProjectWork reports whether an interpreter invocation is
// project work (`python -m pytest`, `node script.js`, `python script.py`) rather
// than an ad-hoc one-liner (`python -c '…'`, a bare REPL). The `-c`/`-e` forms are
// deliberately not covered by the typed surface: they are the guarded hatch.
func bashInterpreterRunsProjectWork(arguments []string) bool {
	if len(arguments) == 0 {
		return false
	}
	for _, argument := range arguments {
		if argument == "-c" || argument == "-e" || argument == "--eval" || argument == "-i" || argument == "--interactive" {
			return false
		}
	}
	for _, argument := range arguments {
		if argument == "-m" || argument == "--module" {
			return true
		}
	}
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") || strings.Contains(argument, "=") {
			continue
		}
		for _, extension := range bashScriptExtensions {
			if strings.HasSuffix(argument, extension) {
				return true
			}
		}
	}
	return false
}

// BashCoverageReport aggregates the classification of many `coder_bash` calls: how
// many fell into each category and how much of that traffic the typed tool surface
// already covers (ci#514 - the coverage metric CI publishes per PR).
type BashCoverageReport struct {
	// Total is the number of classified commands.
	Total int `json:"total"`
	// ByCategory counts the commands per category (only non-zero entries).
	ByCategory map[string]int `json:"by_category"`
	// TypedCoverable is how many commands a typed tool could replace.
	TypedCoverable int `json:"typed_coverable"`
	// UntypedExamples holds up to BashCoverageMaxExamples unclassified commands,
	// so the residual `coder_bash` shapes stay visible instead of just counted.
	UntypedExamples []string `json:"untyped_examples,omitempty"`
	// WrittenVerifications counts the mutating typed GitHub writes that were
	// re-read and reported as verified during the turn (ci#514 phase 1 exit
	// criterion 4): writes are verified by read-back instead of claimed.
	// An unset (zero) value means "no recorded verification evidence yet",
	// which the criteria evaluator treats as unattested rather than assumed.
	WrittenVerifications int `json:"written_verifications,omitempty"`
	// WrittenRecorded is the denominator WrittenCoverageFraction divides by:
	// every mutating typed GitHub write that was recorded during the turn,
	// whether or not its read-back verified it yet. It is the per-record
	// persistence of the "0 vs 1" stub WrittenCoverageFraction replaced.
	WrittenRecorded int `json:"written_recorded,omitempty"`
	// WrittenUnverified counts the recorded writes that are still waiting on a
	// verified read-back (the residual the phase-1 exit criterion works off).
	WrittenUnverified int `json:"written_unverified,omitempty"`
}

// BashCoverageMaxExamples is how many residual (untyped) commands a report keeps.
const BashCoverageMaxExamples = 5

// GitHubWriteVerificationsFromMessages derives the per-record write verdicts of
// a turn straight from the same assistant transcript the coverage report
// classifies: every mutating typed GitHub tool call is a recorded write, and a
// later tool-result read-back that mentions the write's payload (the body_or_call
// the write produced) closes it as verified. It is the transcript pivot nativecoder's
// read-back modal already works in - a verdict row per recorded mutating write,
// verified only when the read-back re-read it. A malformed or missing read-back
// leaves the record unverified rather than inventing a verification, so the
// criterion's "verified instead of claimed" never sees a claimed write.
func GitHubWriteVerificationsFromMessages(messages []map[string]interface{}) []GitHubWriteVerificationResult {
	var verdicts []GitHubWriteVerificationResult
	for _, message := range messages {
		if roleOf(message) != "assistant" {
			continue
		}
		for _, call := range toolCallsOf(message) {
			if function, ok := call["function"].(map[string]interface{}); ok {
				name, _ := function["name"].(string)
				if readTool, writeHint := GitHubWriteVerificationFor(name); readTool != "" && writeHint != "" {
					verdicts = append(verdicts, githubWriteVerificationFromToolCall(call, name, messages))
				}
			}
		}
	}
	return verdicts
}

// toolCallArgumentsOf extracts the raw JSON arguments string of one tool call
// record, mirroring how BashCommandsFromMessages reads `function.arguments`.
func toolCallArgumentsOf(call map[string]interface{}) (string, bool) {
	if function, ok := call["function"].(map[string]interface{}); ok {
		if arguments, ok := function["arguments"].(string); ok {
			return arguments, true
		}
	}
	arguments, _ := call["arguments"].(string)
	return arguments, arguments != ""
}

// firstString reads the first non-empty string value for key off a parsed JSON
// payload map, or "".
func firstString(payload map[string]interface{}, key string) string {
	if raw, ok := payload[key]; ok {
		if value, ok := raw.(string); ok {
			return value
		}
	}
	return ""
}

// githubWriteVerificationFromToolCall resolves one recorded mutating typed GitHub
// write into its verdict by matching the write's payload (the body_or_call string
// the writes layer injects) against the read-back tool results.
func githubWriteVerificationFromToolCall(call map[string]interface{}, tool string, messages []map[string]interface{}) GitHubWriteVerificationResult {
	arguments, _ := toolCallArgumentsOf(call)
	var payload map[string]interface{}
	_ = json.Unmarshal([]byte(arguments), &payload)
	body := strings.TrimSpace(firstString(payload, "body_or_call"))
	if body == "" {
		body = strings.TrimSpace(firstString(payload, "command"))
	}
	if body == "" {
		if rawCommands, ok := payload["commands"].([]interface{}); ok {
			for _, entry := range rawCommands {
				if typed, ok := entry.(string); ok {
					if trimmed := strings.TrimSpace(typed); trimmed != "" {
						body = trimmed
						break
					}
				}
			}
		}
	}
	readTool, _ := GitHubWriteVerificationFor(tool)
	verdict := GitHubWriteVerificationResult{
		Tool:     tool,
		ReadTool: readTool,
		Body:     body,
		Reason:   "no matching read-back yet",
	}
	if body != "" {
		for _, message := range messages {
			if roleOf(message) != "tool" {
				continue
			}
			content, _ := message["content"].(string)
			if content != "" && strings.Contains(content, body) {
				verdict.Verified = true
				verdict.Reason = ""
				break
			}
		}
	}
	return verdict
}

// GitHubWriteVerificationsSummary aggregates the verdict rows of a turn into
// the counted columns the coverage report and the metadata render. It is the
// pure, dependency-free bridge between the per-record read-back verdicts
// (github_writes.go) and the report's WrittenRecorded / WrittenVerifications /
// WrittenUnverified counts, so the api layer stays measurement-only and the
// criterion evaluator keeps consuming the same row shape as before.
func GitHubWriteVerificationsSummary(verdicts []GitHubWriteVerificationResult) (recorded, verified, unverified int) {
	for _, verdict := range verdicts {
		recorded++
		if verdict.Verified {
			verified++
		} else {
			unverified++
		}
	}
	return recorded, verified, unverified
}

// ApplyGitHubWriteVerifications folds the per-record read-back verdicts of the
// turn into the coverage report: WrittenRecorded counts every recorded mutating
// typed GitHub write, WrittenVerifications counts the ones the read-back
// confirmed, and the leftover records are kept visible in WrittenUnverified so
// "writes still waiting on a read-back" stays a number, not a gap. The report
// is returned unchanged when no verdicts are supplied (the empty-report case
// must keep its zero values so the criteria evaluator treats a chat without
// evidence as unattested, not as all-verified).
func (r BashCoverageReport) ApplyGitHubWriteVerifications(verdicts []GitHubWriteVerificationResult) BashCoverageReport {
	recorded, verified, unverified := GitHubWriteVerificationsSummary(verdicts)
	r.WrittenRecorded = recorded
	r.WrittenVerifications = verified
	r.WrittenUnverified = unverified
	return r
}

// BuildBashCoverageReport classifies every command of a turn and aggregates the
// buckets into the coverage report.
func BuildBashCoverageReport(commands []string) BashCoverageReport {
	report := BashCoverageReport{ByCategory: map[string]int{}}
	for _, command := range commands {
		if strings.TrimSpace(command) == "" {
			continue
		}
		category := ClassifyBashCommand(command)
		report.Total++
		report.ByCategory[string(category)]++
		if len(BashCategoryTypedTools[category]) > 0 {
			report.TypedCoverable++
			continue
		}
		if len(report.UntypedExamples) < BashCoverageMaxExamples {
			report.UntypedExamples = append(report.UntypedExamples, strings.TrimSpace(command))
		}
	}
	return report
}

// TypedCoverageFraction is the share of classified commands a typed tool could
// replace (0 when nothing was classified yet).
func (r BashCoverageReport) TypedCoverageFraction() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.TypedCoverable) / float64(r.Total)
}

// TypedCoveragePercent is the same value rounded for a human/JSON report.
func (r BashCoverageReport) TypedCoveragePercent() int {
	if r.Total == 0 {
		return 0
	}
	return r.TypedCoverable * 100 / r.Total
}

// Categories returns the categories present in the report, most frequent first,
// so the payload reads like the ticket evidence instead of a random map order.
func (r BashCoverageReport) Categories() []string {
	names := make([]string, 0, len(r.ByCategory))
	for name := range r.ByCategory {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if r.ByCategory[names[i]] != r.ByCategory[names[j]] {
			return r.ByCategory[names[i]] > r.ByCategory[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

// WrittenCoverageFraction is the share of recorded typed GitHub writes that
// were verified by their read-back. It is the phase-1 exit-criterion-4 metric:
// writes verified instead of claimed. WrittenVerifications is the numerator
// (the count of read-back verified writes); WrittenRecorded is the denominator
// (every mutating typed GitHub write that was recorded, verified or not), so
// 0 means "no writes verified yet" and 1 means "every recorded write was
// verified" — never a guess: with nothing recorded the metric stays 0
// (unattested), and the criteria evaluator treats that as no-exit-evidence.
func (r BashCoverageReport) WrittenCoverageFraction() float64 {
	if r.WrittenRecorded == 0 {
		return 0
	}
	if r.WrittenVerifications >= r.WrittenRecorded {
		return 1
	}
	return float64(r.WrittenVerifications) / float64(r.WrittenRecorded)
}

// WrittenCoveragePercent is WrittenCoverageFraction rounded for the JSON
// metadata, so the payload reads like the ticket evidence (0..100).
func (r BashCoverageReport) WrittenCoveragePercent() int {
	fraction := r.WrittenCoverageFraction()
	if fraction >= 1 {
		return 100
	}
	return int(fraction * 100)
}

// Metadata renders the coverage report as JSON-friendly state, so the
// `coder_context` payload can carry the per-turn bash coverage (ci#514 S1).
func (r BashCoverageReport) Metadata() map[string]interface{} {
	byCategory := map[string]interface{}{}
	for name, count := range r.ByCategory {
		byCategory[name] = count
	}
	meta := map[string]interface{}{
		"total":                    r.Total,
		"by_category":              byCategory,
		"typed_coverable":          r.TypedCoverable,
		"typed_coverage_percent":   r.TypedCoveragePercent(),
		"untyped_examples":         r.UntypedExamples,
		"written_verifications":    r.WrittenVerifications,
		"written_recorded":         r.WrittenRecorded,
		"written_unverified":       r.WrittenUnverified,
		"written_coverage_percent": r.WrittenCoveragePercent(),
	}
	return meta
}

// bashToolCallArguments is the `coder_bash` input the classifier understands
// (`command` for a single call, `commands` for a batch).
type bashToolCallArguments struct {
	Command  string        `json:"command"`
	Commands []interface{} `json:"commands"`
}

// BashCommandsFromMessages extracts the shell command lines of every `coder_bash`
// tool call in a message list, so the per-turn coverage report can be built from
// the same history the context budget measures.
func BashCommandsFromMessages(messages []map[string]interface{}) []string {
	commands := []string{}
	for _, message := range messages {
		if roleOf(message) != "assistant" {
			continue
		}
		for _, call := range toolCallsOf(message) {
			name := ""
			arguments := ""
			if function, ok := call["function"].(map[string]interface{}); ok {
				name, _ = function["name"].(string)
				arguments, _ = function["arguments"].(string)
			}
			if name == "" {
				name, _ = call["name"].(string)
			}
			if strings.TrimSpace(name) != "coder_bash" {
				continue
			}
			commands = append(commands, parseBashToolCommands(arguments)...)
		}
	}
	return commands
}

// parseBashToolCommands reads the command lines out of a `coder_bash` argument
// JSON object. A malformed payload is treated as no command at all.
func parseBashToolCommands(arguments string) []string {
	if strings.TrimSpace(arguments) == "" {
		return nil
	}
	var parsed bashToolCallArguments
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return nil
	}
	commands := []string{}
	if trimmed := strings.TrimSpace(parsed.Command); trimmed != "" {
		commands = append(commands, trimmed)
	}
	for _, entry := range parsed.Commands {
		switch typed := entry.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				commands = append(commands, trimmed)
			}
		case map[string]interface{}:
			// A batch entry may be an object with its own `command`/`commands`.
			if nested, ok := typed["command"].(string); ok {
				if trimmed := strings.TrimSpace(nested); trimmed != "" {
					commands = append(commands, trimmed)
				}
			}
		}
	}
	return commands
}

// BashSteeringHint returns the typed alternative to suggest for a shell command,
// or "" when the classifier knows no typed replacement (`other`). It is the shared
// wording of the ci#514 S2 steering hints.
func BashSteeringHint(command string) string {
	category := ClassifyBashCommand(command)
	tools := BashCategoryTypedTools[category]
	if len(tools) == 0 {
		return ""
	}
	return "This looks like " + string(category) + " work: prefer the typed tool (" + strings.Join(tools, ", ") + ") over coder_bash."
}
