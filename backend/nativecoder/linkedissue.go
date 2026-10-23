package nativecoder

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// This file is the single source of truth for the issue-first workflow
// (ci#813): a feature/bugfix pull request implements exactly one *leaf* issue
// in the private tracker, and the consolidation PR repeats the closing links so
// merging the batch closes the issues.
//
// GitHub cannot auto-close an issue in another repository, so the closing
// keyword has to be recognised by the core (the CI merge gate and the
// consolidation runner) instead of relying on GitHub's own parser.

// LinkedIssueRepo is the repository that owns the leaf issues.
const LinkedIssueRepo = "msgmate-io/open-chat-go-ci"

// linkedIssueLine matches a standalone closing line. Requiring the keyword and
// reference on their own line keeps prose, tables and code samples from being
// mistaken for a closing link. Accepted forms:
//
//	Closes msgmate-io/open-chat-go-ci#123
//	Fixes ci#123
//	Resolves: open-chat-go-ci#123
var linkedIssueLine = regexp.MustCompile(`(?i)^[ \t]*(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?[ \t]+(?:msgmate-io/open-chat-go-ci#|open-chat-go-ci#|ci#)([0-9]+)[ \t]*$`)

// LinkedIssueNumbers returns the unique leaf-issue numbers named by standalone
// closing lines in body, in ascending order.
func LinkedIssueNumbers(body string) []int {
	seen := make(map[int]struct{})
	for _, line := range strings.Split(body, "\n") {
		match := linkedIssueLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if match == nil {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err != nil || number <= 0 {
			continue
		}
		seen[number] = struct{}{}
	}
	out := make([]int, 0, len(seen))
	for number := range seen {
		out = append(out, number)
	}
	sort.Ints(out)
	return out
}

// LinkedIssueLine renders the canonical closing line for an issue.
func LinkedIssueLine(number int) string {
	return fmt.Sprintf("Closes %s#%d", LinkedIssueRepo, number)
}

// mergeLinkedIssues returns the sorted union of the linked issues of an
// existing consolidation and the PRs newly folded into it.
func mergeLinkedIssues(existing []int, include []PullRequest) []int {
	seen := make(map[int]struct{}, len(existing))
	for _, number := range existing {
		if number > 0 {
			seen[number] = struct{}{}
		}
	}
	for _, pr := range include {
		for _, number := range pr.LinkedIssues {
			if number > 0 {
				seen[number] = struct{}{}
			}
		}
	}
	out := make([]int, 0, len(seen))
	for number := range seen {
		out = append(out, number)
	}
	sort.Ints(out)
	return out
}
