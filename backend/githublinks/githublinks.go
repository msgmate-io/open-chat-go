// Package githublinks normalizes GitHub URLs that are relayed to humans.
package githublinks

import (
	"net/url"
	"strings"
)

const (
	gitHubAPIHost   = "api.github.com"
	gitHubWebHost   = "github.com"
	gitHubReposPath = "/repos/"
)

// HumanURL converts a GitHub REST API URL into the equivalent human-browsable
// web URL, so links relayed inside chat messages, coding-agent trigger
// messages and notifications are clickable in the chat UI.
//
//	https://api.github.com/repos/{owner}/{repo}/issues/{n}
//	=> https://github.com/{owner}/{repo}/issues/{n}
//
// Values that are not GitHub API URLs (already human URLs, empty strings,
// unrelated URLs) are returned trimmed and otherwise unchanged, so the helper
// is safe to apply unconditionally.
func HumanURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil {
		return trimmed
	}
	if !strings.EqualFold(parsed.Hostname(), gitHubAPIHost) {
		return trimmed
	}
	path := parsed.EscapedPath()
	if !strings.HasPrefix(path, gitHubReposPath) {
		return trimmed
	}
	rest := strings.Trim(strings.TrimPrefix(path, gitHubReposPath), "/")
	if rest == "" {
		return trimmed
	}
	return "https://" + gitHubWebHost + "/" + rest
}
