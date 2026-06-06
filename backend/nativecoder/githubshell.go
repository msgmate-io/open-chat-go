package nativecoder

import (
	"strconv"
	"strings"
)

// This file implements ci#514, phase 1 (GitHub first): the deterministic bridge
// between the `gh` CLI shapes a dev agent used to run through `coder_bash` and
// the typed `github_*` tool calls that replace them.
//
// It is the executable half of the plan in bashretirement.go: the plan says
// which usage category phase 1 retires and which typed tools retire it; this
// file says how one concrete historical command maps onto exactly one typed
// call, and measures that translation on historical samples so phase 1 cannot
// be declared done while the shell still has to do GitHub work.
//
// The translation is conservative in the same way the classifier is: a `gh`
// verb the typed surface does not cover, an API call with a request body and
// every `gh` command that prints credentials are *not* translated, they are
// reported as residual. Over-claiming would hide exactly the capability loss
// the plan exists to prevent.
//
// Nothing secret is carried: the mapping only forwards repo, number, title,
// body, state and field selection - the arguments the typed tools already take.
// A credential-printing command (`gh auth token`) is explicitly refused, so the
// bridge can never be used to launder a token into a tool result.
//
// Every tool name produced here is asserted to be part of the plan's
// `github_cli` category target surface by githubshell_test.go, so the bridge and
// the plan cannot drift apart.

// The typed GitHub tool names of the ci#514 phase 1 target surface. They are
// exactly the tools the plan's `github_cli` category lists as its target.
const (
	GitHubToolIssueList    = "github_issue_list"
	GitHubToolIssueView    = "github_issue_view"
	GitHubToolIssueCreate  = "github_issue_create"
	GitHubToolIssueComment = "github_issue_comment"
	GitHubToolPRList       = "github_pr_list"
	GitHubToolPRView       = "github_pr_view"
	GitHubToolPRCreate     = "github_pr_create"
	GitHubToolPRComment    = "github_pr_comment"
	GitHubToolPRReview     = "github_pr_review"
	GitHubToolPRMerge      = "github_pr_merge"
	GitHubToolAuthStatus   = "github_auth_status"
	GitHubToolAPI          = "github_api"
)

// GitHubToolCall is one typed call a historical shell command translates into.
type GitHubToolCall struct {
	// Tool is the typed tool name (one of the GitHubTool* constants).
	Tool string `json:"tool"`
	// Arguments are the typed tool's input arguments.
	Arguments map[string]interface{} `json:"arguments"`
	// Mutating is true for calls that change GitHub state; the runtime keeps
	// those on the confirmation path, exactly like the shell command did.
	Mutating bool `json:"mutating"`
	// Admin marks the one admin-scoped write (merging a pull request).
	Admin bool `json:"admin,omitempty"`
	// Reason is the short human explanation shown to the model, so the typed
	// call replaces the shell without a capability or clarity loss.
	Reason string `json:"reason"`
}

// GitHubShellTranslation is the outcome for one historical command: either a
// typed call, or a residual (the shell keeps the work and phase 1 stays open).
type GitHubShellTranslation struct {
	Command    string          `json:"command"`
	Translated bool            `json:"translated"`
	Residual   bool            `json:"residual"`
	Call       *GitHubToolCall `json:"call,omitempty"`
	Reason     string          `json:"reason"`
}

// GitHubShellTranslationReport is the phase 1 readiness metric: over a set of
// historical shell-call samples, how much of the GitHub work the typed surface
// can already express, and whether the phase can be closed.
type GitHubShellTranslationReport struct {
	PlanVersion        int            `json:"plan_version"`
	SourceTool         string         `json:"source_tool"`
	Threshold          float64        `json:"threshold"`
	TotalCalls         int            `json:"total_calls"`
	ErroredCalls       int            `json:"errored_calls"`
	Commands           int            `json:"commands"`
	GitHubCommands     int            `json:"github_commands"`
	TranslatedCommands int            `json:"translated_commands"`
	ResidualCommands   int            `json:"residual_commands"`
	TranslationRatio   float64        `json:"translation_ratio"`
	GitHubShare        float64        `json:"github_share"`
	PhaseReady         bool           `json:"phase_ready"`
	ResidualExamples   []string       `json:"residual_examples,omitempty"`
	ResidualReasons    map[string]int `json:"residual_reasons,omitempty"`
}

// maxGitHubResidualExamples caps the per-report residual command list, so a
// report stays readable no matter how many samples it is run over.
const maxGitHubResidualExamples = 5

// IsGitHubShellCommand reports whether a command is a GitHub CLI invocation
// (`gh ...`), optionally behind leading environment assignments.
func IsGitHubShellCommand(command string) bool {
	words := ghShellWords(command)
	return len(words) > 0 && words[0] == "gh"
}

// TranslateGitHubShellCommand translates one shell command into the typed
// `github_*` call that replaces it. It never returns an error: a command it
// cannot express is returned as a residual with the reason, which is what the
// phase 1 exit criterion is measured on.
func TranslateGitHubShellCommand(command string) GitHubShellTranslation {
	trimmed := strings.TrimSpace(command)
	translation := GitHubShellTranslation{Command: trimmed}
	words := ghShellWords(trimmed)
	if len(words) == 0 || words[0] != "gh" {
		translation.Residual = true
		translation.Reason = "not a GitHub CLI invocation: nothing for the phase 1 bridge to translate"
		return translation
	}
	if len(words) < 2 {
		translation.Residual = true
		translation.Reason = "a bare `gh` has no typed equivalent"
		return translation
	}
	var (
		call   GitHubToolCall
		ok     bool
		reason string
	)
	rest := words[1:]
	switch rest[0] {
	case "issue":
		call, ok, reason = translateGitHubIssueShell(rest[1:])
	case "pr":
		call, ok, reason = translateGitHubPRShell(rest[1:])
	case "auth":
		call, ok, reason = translateGitHubAuthShell(rest[1:])
	case "api":
		call, ok, reason = translateGitHubAPIShell(rest[1:])
	default:
		reason = "`gh " + rest[0] + "` has no typed equivalent in the phase 1 surface"
	}
	if !ok {
		translation.Residual = true
		translation.Reason = reason
		return translation
	}
	translation.Translated = true
	translation.Call = &call
	if strings.TrimSpace(reason) != "" {
		translation.Reason = reason
	} else {
		translation.Reason = call.Reason
	}
	return translation
}

// MeasureGitHubShellTranslation runs the phase 1 bridge over historical shell
// call samples and reports how much of the GitHub work is expressible by typed
// tools. Samples whose arguments cannot be parsed are reported separately and
// are never counted as translated.
func MeasureGitHubShellTranslation(samples []BashCallSample) GitHubShellTranslationReport {
	plan := BashRetirementPlanSnapshot()
	report := GitHubShellTranslationReport{
		PlanVersion:     plan.Version,
		SourceTool:      plan.DeprecatedTool,
		Threshold:       plan.CoverageThreshold,
		TotalCalls:      len(samples),
		ResidualReasons: map[string]int{},
	}
	for _, sample := range samples {
		commands, err := ExtractShellCommands(sample.Tool, sample.Arguments)
		if err != nil {
			report.ErroredCalls++
			continue
		}
		for _, command := range commands {
			report.Commands++
			if !IsGitHubShellCommand(command) {
				continue
			}
			report.GitHubCommands++
			translation := TranslateGitHubShellCommand(command)
			if translation.Translated {
				report.TranslatedCommands++
				continue
			}
			report.ResidualCommands++
			report.ResidualReasons[translation.Reason]++
			if len(report.ResidualExamples) < maxGitHubResidualExamples {
				report.ResidualExamples = append(report.ResidualExamples, translation.Command)
			}
		}
	}
	if report.GitHubCommands > 0 {
		report.TranslationRatio = float64(report.TranslatedCommands) / float64(report.GitHubCommands)
	}
	if report.Commands > 0 {
		report.GitHubShare = float64(report.GitHubCommands) / float64(report.Commands)
	}
	// Phase 1 closes when every observed GitHub command has a typed equivalent
	// and the category has dropped below the plan's share threshold.
	report.PhaseReady = report.Commands > 0 &&
		report.ResidualCommands == 0 &&
		report.GitHubShare < plan.CoverageThreshold
	if len(report.ResidualReasons) == 0 {
		report.ResidualReasons = nil
	}
	return report
}

func newGitHubToolCall(tool, reason string) GitHubToolCall {
	return GitHubToolCall{Tool: tool, Arguments: map[string]interface{}{}, Reason: reason}
}

// translateGitHubIssueShell maps `gh issue …` verbs onto the typed issue tools.
func translateGitHubIssueShell(args []string) (GitHubToolCall, bool, string) {
	if len(args) == 0 {
		return GitHubToolCall{}, false, "`gh issue` without a verb has no typed equivalent"
	}
	verb := args[0]
	positional, values, _ := ghShellFlags(args[1:])
	repo := values["repo"]
	fields := splitShellCSV(values["json"])
	switch verb {
	case "list":
		call := newGitHubToolCall(GitHubToolIssueList, "reads the tracker and returns the selected fields as structured data")
		setShellArg(call.Arguments, "repo", repo)
		setShellArg(call.Arguments, "state", values["state"])
		addShellInt(call.Arguments, "limit", values["limit"])
		if len(fields) > 0 {
			call.Arguments["fields"] = fields
		}
		return call, true, ""
	case "view":
		number, ok := firstShellNumber(positional)
		if !ok {
			return GitHubToolCall{}, false, "`gh issue view` needs an issue number"
		}
		call := newGitHubToolCall(GitHubToolIssueView, "reads one issue, including its body, without a shell")
		call.Arguments["number"] = number
		setShellArg(call.Arguments, "repo", repo)
		if len(fields) > 0 {
			call.Arguments["fields"] = fields
		}
		return call, true, ""
	case "create":
		call := newGitHubToolCall(GitHubToolIssueCreate, "creates the issue and returns its number and URL instead of printing a URL to scrape")
		call.Mutating = true
		setShellArg(call.Arguments, "repo", repo)
		setShellArg(call.Arguments, "title", values["title"])
		setShellArg(call.Arguments, "body", values["body"])
		return call, true, ""
	case "comment":
		number, ok := firstShellNumber(positional)
		if !ok {
			return GitHubToolCall{}, false, "`gh issue comment` needs an issue number"
		}
		call := newGitHubToolCall(GitHubToolIssueComment, "posts the comment and returns its id, so the post is verifiable instead of claimed")
		call.Mutating = true
		call.Arguments["number"] = number
		setShellArg(call.Arguments, "repo", repo)
		setShellArg(call.Arguments, "body", values["body"])
		return call, true, ""
	}
	return GitHubToolCall{}, false, "`gh issue " + verb + "` is not covered by the phase 1 surface"
}

// translateGitHubPRShell maps `gh pr …` verbs onto the typed pull request
// tools. `gh pr diff` and `gh pr checks` stay on `github_pr_view` with the
// extra section requested, so one tool answers "what is this PR?" completely.
func translateGitHubPRShell(args []string) (GitHubToolCall, bool, string) {
	if len(args) == 0 {
		return GitHubToolCall{}, false, "`gh pr` without a verb has no typed equivalent"
	}
	verb := args[0]
	positional, values, flags := ghShellFlags(args[1:])
	repo := values["repo"]
	fields := splitShellCSV(values["json"])
	switch verb {
	case "list":
		call := newGitHubToolCall(GitHubToolPRList, "lists the open pull requests as structured rows")
		setShellArg(call.Arguments, "repo", repo)
		setShellArg(call.Arguments, "state", values["state"])
		addShellInt(call.Arguments, "limit", values["limit"])
		if len(fields) > 0 {
			call.Arguments["fields"] = fields
		}
		return call, true, ""
	case "view", "diff", "checks":
		number, ok := firstShellNumber(positional)
		if !ok {
			return GitHubToolCall{}, false, "`gh pr " + verb + "` needs a pull request number"
		}
		reason := "reads one pull request without a shell"
		call := newGitHubToolCall(GitHubToolPRView, reason)
		call.Arguments["number"] = number
		setShellArg(call.Arguments, "repo", repo)
		if len(fields) > 0 {
			call.Arguments["fields"] = fields
		}
		switch verb {
		case "diff":
			call.Arguments["include_diff"] = true
			call.Reason = "reads one pull request and its diff, so the model never re-parses a diff piped through the shell"
		case "checks":
			call.Arguments["include_checks"] = true
			call.Reason = "reads one pull request and its check rollup as structured statuses"
		}
		return call, true, ""
	case "create":
		call := newGitHubToolCall(GitHubToolPRCreate, "creates the pull request against the base branch and returns its number and URL")
		call.Mutating = true
		setShellArg(call.Arguments, "repo", repo)
		setShellArg(call.Arguments, "title", values["title"])
		setShellArg(call.Arguments, "body", values["body"])
		setShellArg(call.Arguments, "base", values["base"])
		setShellArg(call.Arguments, "head", values["head"])
		if flags["draft"] {
			call.Arguments["draft"] = true
		}
		return call, true, ""
	case "comment":
		number, ok := firstShellNumber(positional)
		if !ok {
			return GitHubToolCall{}, false, "`gh pr comment` needs a pull request number"
		}
		call := newGitHubToolCall(GitHubToolPRComment, "posts the comment on the pull request and returns its id")
		call.Mutating = true
		call.Arguments["number"] = number
		setShellArg(call.Arguments, "repo", repo)
		setShellArg(call.Arguments, "body", values["body"])
		return call, true, ""
	case "review":
		number, ok := firstShellNumber(positional)
		if !ok {
			return GitHubToolCall{}, false, "`gh pr review` needs a pull request number"
		}
		event := ""
		switch {
		case flags["approve"]:
			event = "APPROVE"
		case flags["request-changes"]:
			event = "REQUEST_CHANGES"
		case flags["comment"]:
			event = "COMMENT"
		}
		if event == "" {
			return GitHubToolCall{}, false, "`gh pr review` without --approve/--request-changes/--comment is ambiguous"
		}
		call := newGitHubToolCall(GitHubToolPRReview, "posts the review with an explicit event, so the verdict is a typed field")
		call.Mutating = true
		call.Arguments["number"] = number
		setShellArg(call.Arguments, "repo", repo)
		setShellArg(call.Arguments, "body", values["body"])
		call.Arguments["event"] = event
		return call, true, ""
	case "merge":
		number, ok := firstShellNumber(positional)
		if !ok {
			return GitHubToolCall{}, false, "`gh pr merge` needs a pull request number"
		}
		method := "merge"
		switch {
		case flags["squash"]:
			method = "squash"
		case flags["rebase"]:
			method = "rebase"
		case flags["merge"]:
			method = "merge"
		}
		call := newGitHubToolCall(GitHubToolPRMerge, "merges the green pull request through the admin-scoped tool, which is the only place the merge policy lives")
		call.Mutating = true
		call.Admin = true
		call.Arguments["number"] = number
		setShellArg(call.Arguments, "repo", repo)
		call.Arguments["method"] = method
		if flags["delete-branch"] {
			call.Arguments["delete_branch"] = true
		}
		return call, true, ""
	}
	return GitHubToolCall{}, false, "`gh pr " + verb + "` is not covered by the phase 1 surface"
}

// translateGitHubAuthShell maps `gh auth status` onto the typed read. Anything
// that prints a credential (`gh auth token`) is refused outright: the bridge
// must never be a way to launder a secret into a tool result.
func translateGitHubAuthShell(args []string) (GitHubToolCall, bool, string) {
	if len(args) == 0 {
		return GitHubToolCall{}, false, "`gh auth` without a verb has no typed equivalent"
	}
	switch args[0] {
	case "status":
		call := newGitHubToolCall(GitHubToolAuthStatus, "reports the authenticated identity as a typed field, so `authors: [self]` resolution is not a log scrape")
		return call, true, ""
	case "token", "login", "logout", "refresh", "switch", "setup-git":
		return GitHubToolCall{}, false, "`gh auth " + args[0] + "` touches credentials; it is never bridged and stays on the guarded residual"
	}
	return GitHubToolCall{}, false, "`gh auth " + args[0] + "` is not covered by the phase 1 surface"
}

// translateGitHubAPIShell maps read-only `gh api` calls onto the allow-listed
// `github_api` typed tool. Mutating calls (any non-GET method, or a request
// body through -f/-F/--raw-field) are refused, because the long tail is
// exactly where an untyped escape hatch would sneak back in.
func translateGitHubAPIShell(args []string) (GitHubToolCall, bool, string) {
	positional, values, flags := ghShellFlags(args)
	if len(positional) == 0 {
		return GitHubToolCall{}, false, "`gh api` needs a path"
	}
	method := strings.ToUpper(strings.TrimSpace(firstNonEmpty(values["method"], values["X"])))
	if method == "" {
		method = "GET"
	}
	if method != "GET" {
		return GitHubToolCall{}, false, "`gh api -X " + method + "` mutates state; the read-only github_api bridge only covers GET"
	}
	if values["field"] != "" || values["F"] != "" || values["raw-field"] != "" || values["f"] != "" {
		return GitHubToolCall{}, false, "`gh api` with a request body mutates state; the read-only github_api bridge refuses it"
	}
	path := strings.TrimSpace(positional[0])
	if path == "graphql" {
		return GitHubToolCall{}, false, "`gh api graphql` stays on the guarded residual: arbitrary queries are not a typed surface"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	call := newGitHubToolCall(GitHubToolAPI, "read-only allow-listed API read that returns decoded JSON")
	call.Arguments["path"] = path
	call.Arguments["method"] = method
	if flags["paginate"] {
		call.Arguments["paginate"] = true
	}
	setShellArg(call.Arguments, "repo", values["repo"])
	return call, true, ""
}

// ghShellWords tokenises a command for the bridge. Unlike the classifier's
// segment splitter it keeps the leading environment assignments and stops at
// the first shell control operator, because a translation is one command: the
// remaining pipeline is classified separately.
func ghShellWords(command string) []string {
	var (
		words   []string
		current strings.Builder
		quote   rune
		started bool
	)
	flush := func() {
		if started {
			words = append(words, current.String())
			current.Reset()
			started = false
		}
	}
	for _, char := range command {
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
				continue
			}
			current.WriteRune(char)
			started = true
		case char == '\'' || char == '"':
			quote = char
			started = true
		case char == ' ' || char == '\t' || char == '\n':
			flush()
		case char == '|' || char == ';' || char == '&' || char == '(' || char == ')':
			flush()
			return words
		case char == '>':
			flush()
			return words
		default:
			current.WriteRune(char)
			started = true
		}
	}
	flush()
	for len(words) > 0 && words[0] != "gh" && isShellEnvAssignment(words[0]) {
		words = words[1:]
	}
	return words
}

// ghBooleanShellFlags lists the value-less flags the bridge understands. They
// are declared, so `gh pr create --draft <title>` can never mistake the title
// for the `--draft` value; unknown flags keep the conservative
// "value-if-followed-by-a-non-flag" rule.
var ghBooleanShellFlags = map[string]bool{
	"draft":           true,
	"paginate":        true,
	"approve":         true,
	"request-changes": true,
	"comment":         true,
	"merge":           true,
	"squash":          true,
	"rebase":          true,
	"delete-branch":   true,
	"admin":           true,
	"web":             true,
}

// ghShellFlags splits a command's argument tokens into positionals, `--flag
// value` / `--flag=value` values and bare flags. GH_TOKEN=x gh pr view 873
// therefore parses to exactly the same typed call as the bare form.
func ghShellFlags(args []string) (positional []string, values map[string]string, flags map[string]bool) {
	values = map[string]string{}
	flags = map[string]bool{}
	for index := 0; index < len(args); index++ {
		token := args[index]
		if !strings.HasPrefix(token, "-") || token == "-" {
			positional = append(positional, token)
			continue
		}
		name := strings.TrimLeft(token, "-")
		if name == "" {
			continue
		}
		if equals := strings.Index(name, "="); equals >= 0 {
			values[name[:equals]] = name[equals+1:]
			continue
		}
		if ghBooleanShellFlags[name] {
			flags[name] = true
			continue
		}
		if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
			values[name] = args[index+1]
			index++
			continue
		}
		flags[name] = true
	}
	return positional, values, flags
}

// isShellEnvAssignment reports whether a token is a leading `KEY=value`
// environment assignment rather than the command name.
func isShellEnvAssignment(token string) bool {
	if token == "" || strings.HasPrefix(token, "-") {
		return false
	}
	equals := strings.Index(token, "=")
	if equals <= 0 {
		return false
	}
	for _, char := range token[:equals] {
		valid := char == '_' ||
			(char >= 'A' && char <= 'Z') ||
			(char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9')
		if !valid {
			return false
		}
	}
	return true
}

func setShellArg(arguments map[string]interface{}, key, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		arguments[key] = trimmed
	}
}

func addShellInt(arguments map[string]interface{}, key, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	if parsed, err := strconv.Atoi(trimmed); err == nil {
		arguments[key] = parsed
	}
}

func firstShellNumber(positional []string) (int, bool) {
	for _, token := range positional {
		if number, err := strconv.Atoi(strings.TrimSpace(token)); err == nil {
			return number, true
		}
	}
	return 0, false
}

func splitShellCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
