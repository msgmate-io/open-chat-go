package nativecoder

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file holds the pure decoders the mention-triggered review bootstrap
// (ci#795, see mentionreview.go) uses to read a GitHub event payload. They are
// deliberately dependency free: a dispatch carries the raw webhook body either
// already decoded (`config.github_event` as an object, eg from the core) or as
// JSON text (as an HTTP client would send it), and only the repository identity
// and the pull request number/refs are needed to prepare the review workspace.

// repoURLHosts are the hosts a repository URL in a GitHub event may point at;
// the parse only needs to drop the host, so a short list keeps the decoder from
// treating an arbitrary URL as a repository.
var repoURLHosts = []string{"github.com", "www.github.com", "api.github.com"}

// repositoryObjectRepo resolves `owner/name` from the `repository` object of a
// GitHub event: `full_name` when present, otherwise `name` combined with
// `owner.login`/`owner.name`/`organization.login`.
func repositoryObjectRepo(event map[string]interface{}) string {
	repo, ok := event["repository"].(map[string]interface{})
	if !ok {
		return ""
	}
	if full, ok := repo["full_name"].(string); ok {
		if normalized := NormalizeRepo(full); normalized != "" {
			return normalized
		}
	}
	name, _ := repo["name"].(string)
	if strings.TrimSpace(name) == "" {
		return ""
	}
	for _, key := range []string{"owner", "organization"} {
		owner, ok := repo[key].(map[string]interface{})
		if !ok {
			continue
		}
		login, _ := owner["login"].(string)
		if strings.TrimSpace(login) == "" {
			login, _ = owner["name"].(string)
		}
		if strings.TrimSpace(login) == "" {
			continue
		}
		if normalized := NormalizeRepo(login + "/" + name); normalized != "" {
			return normalized
		}
	}
	return ""
}

// repoFromURL resolves `owner/name` from a repository URL. It accepts
//
//   - a web URL with a path beyond the repository
//     (`https://github.com/owner/name/pull/1327` -> `owner/name`, which a
//     naive last-two-segments parse would read as `pull/1327`),
//   - the API form (`https://api.github.com/repos/owner/name`),
//   - a scp-style remote (`git@github.com:owner/name.git`),
//   - a bare `owner/name` slug the event carried directly.
//
// It returns "" for a value that names no repository, so a caller keeps looking.
func repoFromURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	path := ""
	for _, host := range repoURLHosts {
		for _, marker := range []string{host + "/", host + ":"} {
			if index := strings.Index(lower, marker); index >= 0 {
				path = trimmed[index+len(marker):]
				break
			}
		}
		if path != "" {
			break
		}
	}
	if path == "" {
		// No host: a value that is exactly an `owner/name` slug is still usable,
		// anything else (a bare word, a deeper path) is not.
		if strings.Count(trimmed, "/") != 1 {
			return ""
		}
		return NormalizeRepo(trimmed)
	}
	path = strings.TrimSuffix(path, ".git")
	segments := splitPathSegments(path)
	// `api.github.com/repos/<owner>/<name>` carries an extra leading segment.
	if len(segments) > 0 && segments[0] == "repos" {
		segments = segments[1:]
	}
	if len(segments) < 2 {
		return ""
	}
	return NormalizeRepo(segments[0] + "/" + segments[1])
}

// splitPathSegments splits a URL path into its non-empty segments, dropping any
// query string or fragment a caller left attached.
func splitPathSegments(path string) []string {
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	parts := strings.Split(path, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

// numberFromValue reads an integer out of a JSON number (float64), a Go int or
// a numeric string, so a pull request number survives either decoding path.
func numberFromValue(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed <= 0 {
			return 0, false
		}
		return int(typed), true
	case int:
		if typed <= 0 {
			return 0, false
		}
		return typed, true
	case int64:
		if typed <= 0 {
			return 0, false
		}
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil || parsed <= 0 {
			return 0, false
		}
		return int(parsed), true
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err != nil {
			return 0, false
		}
		if parsed <= 0 {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// decodeReviewEvent parses a JSON-encoded GitHub event payload. A value that is
// not a JSON object is reported as not decoded so the caller keeps looking for a
// decoded payload instead of failing the dispatch over a malformed config.
func decodeReviewEvent(raw string) (map[string]interface{}, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}
