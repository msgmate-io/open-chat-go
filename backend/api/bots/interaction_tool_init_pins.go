package bots

import (
	"backend/api/msgmate"
	"backend/coordination"
	"backend/database"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ToolNameGithubIssueComment is the typed write tool whose target the server
// pins: a bot that is granted it may comment on the dispatching issue and
// nowhere else (ci#1049).
const ToolNameGithubIssueComment = "github_issue_comment"

// ToolInitPinsFromTrigger derives the server-owned tool-init pins for an
// interaction from its dispatch trigger: the `repo:<owner/name>` and
// `issue:<n>` / `ci#<n>` dispatch tags first (the values the dispatcher stamped
// from the triggering event), then the interaction's post-action repo/number as
// the fallback the CI bot itself configured. When neither names a target the
// interaction gets no pin at all: fail closed, so the write tool is simply not
// granted instead of being granted with a caller-chosen target.
func ToolInitPinsFromTrigger(tags []string, config map[string]interface{}) map[string]map[string]interface{} {
	repo, number := toolInitPinTarget(tags, config)
	if repo == "" || number == "" {
		return map[string]map[string]interface{}{}
	}
	return map[string]map[string]interface{}{
		ToolNameGithubIssueComment: {
			database.ToolInitPinnedFieldRepo:        repo,
			database.ToolInitPinnedFieldIssueNumber: number,
		},
	}
}

// toolInitPinTarget resolves the (repo, issue number) an interaction is bound
// to. Tags win over the post-action; an empty result means "unidentified".
func toolInitPinTarget(tags []string, config map[string]interface{}) (string, string) {
	repo := repoFromDispatchTags(tags)
	number := ticketNumberFromDispatchTags(tags)
	if repo != "" && number != "" {
		return repo, number
	}
	actionRepo, actionNumber := repoAndNumberFromPostActions(config)
	if repo == "" {
		repo = actionRepo
	}
	if number == "" && actionNumber != "" {
		number = actionNumber
	}
	return repo, number
}

// repoFromDispatchTags returns the first `repo:<owner/name>` tag.
func repoFromDispatchTags(tags []string) string {
	for _, tag := range tags {
		value := strings.TrimSpace(tag)
		if !strings.HasPrefix(strings.ToLower(value), "repo:") {
			continue
		}
		if slug := normalizeToolInitPinRepo(strings.TrimPrefix(value, "repo:")); slug != "" {
			return slug
		}
	}
	return ""
}

// ticketNumberFromDispatchTags returns the numeric ticket of the first canonical
// ticket tag (`issue:`, `ticket:` or a bare `ci#`/`gh#` reference).
func ticketNumberFromDispatchTags(tags []string) string {
	ticket, _ := coordination.TicketFromTags(tags)
	return normalizeToolInitPinNumber(ticket)
}

// repoAndNumberFromPostActions reads the repo/number (or the issue URL the repo
// and number can be derived from) of the interaction's configured post action.
func repoAndNumberFromPostActions(config map[string]interface{}) (string, string) {
	if config == nil {
		return "", ""
	}
	entries := []map[string]interface{}{}
	if single, ok := config["post_action"].(map[string]interface{}); ok {
		entries = append(entries, single)
	}
	if list, ok := config["post_actions"].([]interface{}); ok {
		for _, item := range list {
			if entry, ok := item.(map[string]interface{}); ok {
				entries = append(entries, entry)
			}
		}
	}
	for _, entry := range entries {
		repo := normalizeToolInitPinRepo(stringOf(entry["repo"]))
		number := normalizeToolInitPinNumber(stringOf(entry["number"]))
		issueURL := strings.TrimSpace(stringOf(entry["issue"]))
		if issueURL == "" {
			issueURL = strings.TrimSpace(stringOf(entry["issue_url"]))
		}
		if issueURL != "" {
			urlRepo, urlNumber := repoAndNumberFromIssueURL(issueURL)
			if repo == "" {
				repo = urlRepo
			}
			if number == "" {
				number = urlNumber
			}
		}
		if repo != "" && number != "" {
			return repo, number
		}
	}
	return "", ""
}

// repoAndNumberFromIssueURL splits an issue/pull URL into its repo slug and
// number.
func repoAndNumberFromIssueURL(raw string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 {
		return "", ""
	}
	return normalizeToolInitPinRepo(parts[0] + "/" + parts[1]), normalizeToolInitPinNumber(parts[3])
}

// bindInteractionToolInitPins stamps the reserved `tool_init_pins` section onto
// the interaction's shared config. The section is server-owned: any caller
// supplied value is dropped first, so a dispatch cannot pre-seed a pin (or
// suppress one) by shipping the reserved key in its config.
func bindInteractionToolInitPins(config map[string]interface{}, tags []string) map[string]interface{} {
	if config == nil {
		return nil
	}
	delete(config, database.ToolInitPinsKey)
	pins := ToolInitPinsFromTrigger(tags, config)
	if len(pins) == 0 {
		return config
	}
	raw := make(map[string]interface{}, len(pins))
	for toolName, pin := range pins {
		raw[toolName] = pin
	}
	config[database.ToolInitPinsKey] = raw
	return config
}

// grantPinnedWriteTool adds the pinned write tool to the interaction's tool list
// when it is resolvable for this deployment. The grant is the whole point of the
// pin: the tool is only handed out *because* its target is server-bound, so an
// interaction without a pin never gets it (fail closed). A name this deployment
// does not implement is skipped rather than injected, so the grant can never
// turn into a validation failure.
func grantPinnedWriteTool(config map[string]interface{}, pins map[string]map[string]interface{}) {
	if config == nil || len(pins) == 0 {
		return
	}
	if _, pinned := pins[ToolNameGithubIssueComment]; !pinned {
		return
	}
	// The write tool is served by the integration module and registered at
	// startup, so it only resolves when this deployment actually provides it. A
	// name that does not resolve here is skipped rather than injected, so the
	// grant can never turn into a validation failure.
	if _, found := msgmate.NewToolByName(ToolNameGithubIssueComment); !found {
		return
	}
	names, err := collectToolNames(config["tools"])
	if err != nil {
		return
	}
	for _, name := range names {
		if strings.EqualFold(strings.TrimSpace(name), ToolNameGithubIssueComment) {
			return
		}
	}
	granted := make([]interface{}, 0, len(names)+1)
	for _, name := range names {
		granted = append(granted, name)
	}
	granted = append(granted, ToolNameGithubIssueComment)
	config["tools"] = granted
}

// applyInteractionToolInitPins is the single entry point the interaction-creation
// path calls: it binds the server-owned pins from the dispatch trigger and grants
// the pinned write tool only when such a pin exists.
func applyInteractionToolInitPins(config map[string]interface{}, tags []string) map[string]interface{} {
	config = bindInteractionToolInitPins(config, tags)
	grantPinnedWriteTool(config, database.ToolInitPinsFromConfig(config))
	return config
}

func normalizeToolInitPinRepo(repo string) string {
	trimmed := strings.Trim(strings.TrimSpace(repo), "/")
	if trimmed == "" || len(strings.Split(trimmed, "/")) != 2 {
		return ""
	}
	return strings.ToLower(trimmed)
}

func normalizeToolInitPinNumber(number string) string {
	trimmed := strings.TrimSpace(strings.ToLower(number))
	for _, prefix := range []string{"ci#", "gh#", "issue:", "ticket:", "issue/#", "#"} {
		if strings.HasPrefix(trimmed, prefix) {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			break
		}
	}
	if trimmed == "" {
		return ""
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return trimmed
}

// stringOf renders a decoded JSON scalar as a string (numbers included).
func stringOf(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprint(typed)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}
