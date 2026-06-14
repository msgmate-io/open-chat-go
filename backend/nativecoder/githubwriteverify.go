package nativecoder

import (
	"fmt"
	"strings"
)

// ci#514 phase 1 exit criterion: "a write (comment/review/PR) is re-read and
// reported as verified instead of claimed".
//
// githubshell.go turns a `gh` write into a typed *mutating* call, but a
// mutating call on its own only proves that GitHub accepted the request - the
// model could still report "done" for a comment that never appeared. This file
// owns the read-back half of the migration: for every mutating typed GitHub
// tool it names the read-only call that confirms the write, and it interprets
// that read-back so the result says "verified, and this is what I read" or
// "NOT verified, and this is why" - never "claimed".

// GitHubWriteVerification is the read-only follow-up that confirms one mutating
// typed GitHub call, plus the marker the read-back has to contain.
type GitHubWriteVerification struct {
	// Tool is the mutating tool that was called.
	Tool string `json:"tool"`
	// ReadTool is the read-only tool that confirms it.
	ReadTool string `json:"read_tool"`
	// ReadArguments are the read call's arguments (repo, number, fields).
	ReadArguments map[string]interface{} `json:"read_arguments"`
	// Marker is the text the read-back must contain for the write to count as
	// verified. It is empty when the tool has no textual payload of its own (a
	// review): the check is then structural - non-empty and not an error frame -
	// and the verdict says so instead of pretending otherwise.
	Marker string `json:"marker,omitempty"`
	// MarkerFold matches the marker case-insensitively. It is set for state
	// markers that come from the API in a different case (`MERGED`), never for
	// a body or title the agent wrote.
	MarkerFold bool `json:"marker_fold,omitempty"`
	// Reason is the one line the model reads: what was written and where it has
	// to reappear.
	Reason string `json:"reason"`
}

// GitHubWriteVerificationResult is the verdict for one read-back payload.
type GitHubWriteVerificationResult struct {
	Tool     string `json:"tool"`
	ReadTool string `json:"read_tool"`
	Verified bool   `json:"verified"`
	// Weak marks a structurally verified verdict: the read-back succeeded and
	// carries no error frame, but the write carries no marker to match, so
	// nothing proves *this* write landed. It is reported as weakly verified
	// rather than silently folded into `Verified`, because the whole point of
	// the phase is that a claim is distinguishable from a read.
	Weak   bool   `json:"weak,omitempty"`
	Marker string `json:"marker,omitempty"`
	Reason string `json:"reason"`
}

// GitHubWriteVerificationPrefix opens the frame appended to a mutating tool
// result, so the model sees the re-read as part of the write's outcome.
const GitHubWriteVerificationPrefix = "[github write verification] "

// GitHubWriteVerificationFor maps a mutating typed GitHub call onto the
// read-only call that confirms it. number is the number the write returned
// (issue/PR number for a create); it may be zero when the call already carries
// one. A read-only call, or a mutating tool of another family, has no
// verification and returns ok=false: the caller must then keep the write on the
// unverified path rather than invent a read-back.
func GitHubWriteVerificationFor(call GitHubToolCall, number int) (GitHubWriteVerification, bool) {
	if !call.Mutating {
		return GitHubWriteVerification{}, false
	}
	if number == 0 {
		if existing, ok := call.Arguments["number"].(int); ok {
			number = existing
		}
	}
	args := map[string]interface{}{}
	if repo, ok := call.Arguments["repo"].(string); ok && strings.TrimSpace(repo) != "" {
		args["repo"] = repo
	}
	body, _ := call.Arguments["body"].(string)
	title, _ := call.Arguments["title"].(string)
	verification := GitHubWriteVerification{Tool: call.Tool, ReadArguments: args}
	switch call.Tool {
	case GitHubToolIssueComment:
		verification.ReadTool = GitHubToolIssueView
		verification.Marker = body
		verification.Reason = "read issue " + fmt.Sprint(number) + " back and look for the posted comment body; a post that is not on the issue was not posted"
	case GitHubToolPRComment:
		verification.ReadTool = GitHubToolPRView
		verification.Marker = body
		verification.Reason = "read pull request " + fmt.Sprint(number) + " back and look for the posted comment body"
	case GitHubToolIssueCreate:
		verification.ReadTool = GitHubToolIssueView
		verification.Marker = title
		verification.Reason = "read the created issue " + fmt.Sprint(number) + " back and look for its title"
	case GitHubToolPRCreate:
		verification.ReadTool = GitHubToolPRView
		verification.Marker = title
		verification.Reason = "read the created pull request " + fmt.Sprint(number) + " back and look for its title"
	case GitHubToolPRReview:
		verification.ReadTool = GitHubToolPRView
		verification.Reason = "read pull request " + fmt.Sprint(number) + " back and confirm the review state is there"
		if event, ok := call.Arguments["event"].(string); ok {
			// The event is the state the API stores (`APPROVED`,
			// `CHANGES_REQUESTED`, `COMMENTED`); match it case-insensitively
			// because the read-back may print it in either case.
			switch strings.ToUpper(strings.TrimSpace(event)) {
			case "APPROVE":
				verification.Marker, verification.MarkerFold = "APPROVED", true
			case "REQUEST_CHANGES":
				verification.Marker, verification.MarkerFold = "CHANGES_REQUESTED", true
			case "COMMENT":
				verification.Marker, verification.MarkerFold = "COMMENTED", true
			}
		}
	case GitHubToolPRMerge:
		verification.ReadTool = GitHubToolPRView
		verification.Marker, verification.MarkerFold = "merged", true
		verification.Reason = "read pull request " + fmt.Sprint(number) + " back and confirm the read-back reports it as merged"
	default:
		return GitHubWriteVerification{}, false
	}
	if number != 0 {
		verification.ReadArguments["number"] = number
	}
	return verification, true
}

// GitHubWriteVerificationCall renders the read-back half of a verification plan
// as the typed read-only call that performs it. A plan without a read tool has
// no call, so a caller can never invent one.
func GitHubWriteVerificationCall(plan GitHubWriteVerification) (GitHubToolCall, bool) {
	readTool := strings.TrimSpace(plan.ReadTool)
	if readTool == "" {
		return GitHubToolCall{}, false
	}
	call := newGitHubToolCall(readTool, "read-back that verifies "+plan.Tool+": "+plan.Reason)
	for key, value := range plan.ReadArguments {
		call.Arguments[key] = value
	}
	return call, true
}

// VerifyGitHubWrite interprets a read-back payload against the verification
// plan. It is deliberately conservative: an empty payload, an error frame or a
// read-back that does not contain the marker all mean the write is NOT
// verified, and the reason says what was looked for and what came back.
func VerifyGitHubWrite(plan GitHubWriteVerification, readback string) GitHubWriteVerificationResult {
	result := GitHubWriteVerificationResult{
		Tool:     plan.Tool,
		ReadTool: plan.ReadTool,
		Marker:   plan.Marker,
	}
	trimmed := strings.TrimSpace(readback)
	marker := strings.TrimSpace(plan.Marker)
	if trimmed == "" {
		result.Reason = "the read-back is empty: " + plan.Tool + " is NOT verified"
		return result
	}
	if looksLikeToolErrorFrame(trimmed) {
		result.Reason = "the read-back is an error frame, so " + plan.Tool + " is NOT verified: " + firstLine(trimmed)
		return result
	}
	if marker != "" {
		haystack := trimmed
		if plan.MarkerFold {
			haystack = strings.ToLower(haystack)
			marker = strings.ToLower(marker)
		}
		if !strings.Contains(haystack, marker) {
			result.Reason = "the read-back does not contain " + fmt.Sprintf("%q", plan.Marker) + ", so " + plan.Tool + " is NOT verified"
			return result
		}
		result.Verified = true
		result.Reason = "verified: the " + plan.ReadTool + " read-back contains " + fmt.Sprintf("%q", plan.Marker) + " - read, not claimed"
		return result
	}
	// A marker-less write (a review without a matched event) has nothing to
	// match on, so the best the read-back can prove is that the read succeeded.
	// That is *weak* verification and is reported as such: claiming "verified"
	// here would be exactly the unverified claim the phase exists to remove.
	result.Verified = true
	result.Weak = true
	result.Reason = "weakly verified: the " + plan.ReadTool + " read-back is present and carries no error frame, but " + plan.Tool + " has no text or state marker to match - confirm the change yourself before reporting it as done"
	return result
}

// FormatGitHubWriteVerification renders the verdict as the single frame line a
// mutating tool result carries.
func FormatGitHubWriteVerification(result GitHubWriteVerificationResult) string {
	state := "NOT verified"
	switch {
	case result.Verified && result.Weak:
		state = "weakly verified"
	case result.Verified:
		state = "verified"
	}
	return GitHubWriteVerificationPrefix + result.Tool + " is " + state + ": " + result.Reason
}

// looksLikeToolErrorFrame reports whether a payload is the runtime's error frame
// rather than a GitHub read-back.
func looksLikeToolErrorFrame(payload string) bool {
	lower := strings.ToLower(payload)
	return strings.HasPrefix(lower, "error") ||
		strings.HasPrefix(lower, "[tool error]") ||
		strings.Contains(lower, "\"error\":") ||
		strings.Contains(lower, `"error": "`)
}

// firstLine returns the first non-empty line of a payload, for a bounded reason.
func firstLine(payload string) string {
	line := strings.TrimSpace(strings.SplitN(payload, "\n", 2)[0])
	if len(line) > 200 {
		return line[:200] + "..."
	}
	return line
}
