package bots

import (
	"encoding/json"
	"strconv"
	"strings"

	"backend/database"
)

// ci#1049: a bot interaction may post a comment on the issue that triggered it,
// but never anywhere else. The target is server-owned: it is persisted as a
// reserved `tool_init_pins` entry at interaction creation and re-applied at
// execution time, so a model- or client-supplied repo/issue number can never
// repoint the call. With no resolvable trigger issue, no pin is persisted and
// the tool is not granted at all (fail closed).

// pinnedIssueCommentTool is the typed GitHub tool whose target a pin locks.
const pinnedIssueCommentTool = "github_issue_comment"

// triggerIssueBinding is the (repo, issue) an interaction was dispatched for.
type triggerIssueBinding struct {
	Repo        string
	IssueNumber int
}

// valid reports whether the binding names a repo and an issue number.
func (b triggerIssueBinding) valid() bool {
	return strings.Contains(b.Repo, "/") && b.IssueNumber > 0
}

// resolveTriggerIssueBinding derives the triggering issue from the dispatch
// `repo:`/`issue:` tags and falls back to the configured post actions, whose
// repo/number (or issue_url) name the thread the interaction was dispatched
// for. It reports false when nothing names an issue.
func resolveTriggerIssueBinding(tags []string, config map[string]interface{}) (triggerIssueBinding, bool) {
	repo := ""
	for _, tag := range tags {
		value := strings.TrimSpace(tag)
		if repo == "" && len(value) > len("repo:") && strings.EqualFold(value[:len("repo:")], "repo:") {
			repo = strings.TrimSpace(value[len("repo:"):])
		}
	}
	if number, ok := issueNumberFromTags(tags); ok && repo != "" {
		return triggerIssueBinding{Repo: repo, IssueNumber: number}, true
	}
	for _, binding := range postActionBindings(config) {
		if binding.valid() {
			return binding, true
		}
	}
	return triggerIssueBinding{}, false
}

// issueNumberFromTags recovers the issue number from the dispatch ticket tags.
func issueNumberFromTags(tags []string) (int, bool) {
	for _, tag := range tags {
		value := strings.TrimSpace(tag)
		lower := strings.ToLower(value)
		for _, prefix := range []string{"issue:", "ticket:", "ci#", "gh#"} {
			if !strings.HasPrefix(lower, prefix) {
				continue
			}
			number, err := strconv.Atoi(strings.TrimSpace(value[len(prefix):]))
			if err == nil && number > 0 {
				return number, true
			}
		}
	}
	return 0, false
}

// postActionBindings decodes the chat's configured post actions into their
// GitHub targets, preferring an explicit issue_url over the repo/number pair.
func postActionBindings(config map[string]interface{}) []triggerIssueBinding {
	if len(config) == 0 {
		return nil
	}
	raw := make([]map[string]interface{}, 0, 2)
	if single, ok := config["post_action"].(map[string]interface{}); ok {
		raw = append(raw, single)
	}
	if list, ok := config["post_actions"].([]interface{}); ok {
		for _, item := range list {
			if action, isMap := item.(map[string]interface{}); isMap {
				raw = append(raw, action)
			}
		}
	}
	bindings := make([]triggerIssueBinding, 0, len(raw))
	for _, action := range raw {
		blob, err := json.Marshal(action)
		if err != nil {
			continue
		}
		var target postActionGithubComment
		if err := json.Unmarshal(blob, &target); err != nil {
			continue
		}
		if target.Issue != nil {
			if binding, ok := issueBindingFromURL(*target.Issue); ok {
				bindings = append(bindings, binding)
				continue
			}
		}
		bindings = append(bindings, triggerIssueBinding{Repo: strings.TrimSpace(target.Repo), IssueNumber: target.Number})
	}
	return bindings
}

// issueBindingFromURL parses an issue reference: either the web form
// `https://github.com/<owner>/<repo>/issues/<n>` or the API form
// `/repos/<owner>/<repo>/issues/<n>`. The trailing `issues/<n>` segment pair is
// what identifies the number; the two segments before it are the repo.
func issueBindingFromURL(raw string) (triggerIssueBinding, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return triggerIssueBinding{}, false
	}
	if idx := strings.Index(trimmed, "://"); idx >= 0 {
		trimmed = trimmed[idx+3:]
		if slash := strings.Index(trimmed, "/"); slash >= 0 {
			trimmed = trimmed[slash+1:]
		}
	}
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	for idx := 2; idx+1 < len(parts); idx++ {
		if !strings.EqualFold(parts[idx], "issues") {
			continue
		}
		number, err := strconv.Atoi(parts[idx+1])
		if err != nil || number <= 0 {
			return triggerIssueBinding{}, false
		}
		return triggerIssueBinding{Repo: parts[idx-2] + "/" + parts[idx-1], IssueNumber: number}, true
	}
	return triggerIssueBinding{}, false
}

// applyTriggerIssuePinGrant persists the server-owned pin for the interaction's
// triggering issue and grants github_issue_comment only when such a binding
// exists. The input config is never mutated.
func applyTriggerIssuePinGrant(config map[string]interface{}, tags []string) map[string]interface{} {
	binding, ok := resolveTriggerIssueBinding(tags, config)
	if !ok {
		return config
	}
	out := make(map[string]interface{}, len(config)+2)
	for key, value := range config {
		out[key] = value
	}

	pins := map[string]interface{}{}
	if existing, isMap := out[database.ToolInitPinsKey].(map[string]interface{}); isMap {
		for key, value := range existing {
			pins[key] = value
		}
	}
	pins[pinnedIssueCommentTool] = database.BuildGithubIssueToolInitPin(binding.Repo, binding.IssueNumber)
	out[database.ToolInitPinsKey] = pins

	initData := map[string]interface{}{}
	if existing, isMap := out["tool_init"].(map[string]interface{}); isMap {
		for key, value := range existing {
			initData[key] = value
		}
	}
	initData[pinnedIssueCommentTool] = map[string]interface{}{
		"repo":         binding.Repo,
		"issue_number": binding.IssueNumber,
	}
	out["tool_init"] = initData

	// Grant the tool only for an agent config that declares a tool list; a
	// config without tools keeps its compiled-in set (the pin still applies).
	if names, err := collectToolNames(out["tools"]); err == nil && len(names) > 0 {
		granted := false
		for _, name := range names {
			if name == pinnedIssueCommentTool {
				granted = true
				break
			}
		}
		if !granted {
			out["tools"] = append(names, pinnedIssueCommentTool)
		}
	}
	return out
}
