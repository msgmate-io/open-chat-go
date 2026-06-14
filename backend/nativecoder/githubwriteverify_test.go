package nativecoder

import (
	"strings"
	"testing"
)

// mutatingGitHubCalls is one representative call per mutating typed GitHub tool
// of the phase 1 surface, so a new mutating tool cannot ship without a read-back.
func mutatingGitHubCalls() []GitHubToolCall {
	return []GitHubToolCall{
		{Tool: GitHubToolIssueCreate, Mutating: true, Arguments: map[string]interface{}{
			"repo": "msgmate-io/open-chat-go", "title": "a tracked issue",
		}},
		{Tool: GitHubToolIssueComment, Mutating: true, Arguments: map[string]interface{}{
			"repo": "msgmate-io/open-chat-go", "number": 514, "body": "noted on the umbrella",
		}},
		{Tool: GitHubToolPRCreate, Mutating: true, Arguments: map[string]interface{}{
			"repo": "msgmate-io/open-chat-go", "title": "retire the escape hatch",
		}},
		{Tool: GitHubToolPRComment, Mutating: true, Arguments: map[string]interface{}{
			"repo": "msgmate-io/open-chat-go", "number": 873, "body": "reviewer note",
		}},
		{Tool: GitHubToolPRReview, Mutating: true, Arguments: map[string]interface{}{
			"repo": "msgmate-io/open-chat-go", "number": 873, "event": "APPROVE",
		}},
		{Tool: GitHubToolPRMerge, Mutating: true, Arguments: map[string]interface{}{
			"repo": "msgmate-io/open-chat-go", "number": 873,
		}},
	}
}

// Every mutating tool of phase 1 must have a read-back that performs it, so the
// "re-read instead of claimed" exit criterion cannot be satisfied by omission.
func TestGitHubWriteVerificationCoversEveryMutatingTool(t *testing.T) {
	mutating := map[string]bool{}
	for _, call := range mutatingGitHubCalls() {
		mutating[call.Tool] = true
	}
	for _, call := range mutatingGitHubCalls() {
		plan, ok := GitHubWriteVerificationFor(call, 0)
		if !ok {
			t.Fatalf("%s is mutating but has no verification plan", call.Tool)
		}
		if plan.Tool != call.Tool {
			t.Fatalf("plan.Tool = %q, call %q", plan.Tool, call.Tool)
		}
		if plan.Reason == "" {
			t.Fatalf("%s verification has no reason to show the model", call.Tool)
		}
		readCall, ok := GitHubWriteVerificationCall(plan)
		if !ok {
			t.Fatalf("%s verification has no read-back call", call.Tool)
		}
		if readCall.Tool != plan.ReadTool {
			t.Fatalf("%s read-back call is %q, a different tool", call.Tool, readCall.Tool)
		}
		if mutating[readCall.Tool] {
			t.Fatalf("%s read-back %q is itself a mutating tool", call.Tool, readCall.Tool)
		}
	}
}

// A read-only call never invents a read-back, and neither does a mutating tool
// from another family: the caller must keep those on the unverified path.
func TestGitHubWriteVerificationRefusesNonMutatingCalls(t *testing.T) {
	readOnly := []string{GitHubToolIssueView, GitHubToolIssueList, GitHubToolPRView, GitHubToolPRList, GitHubToolAuthStatus}
	for _, tool := range readOnly {
		if _, ok := GitHubWriteVerificationFor(GitHubToolCall{Tool: tool}, 0); ok {
			t.Fatalf("%s is read-only and must not have a write verification", tool)
		}
	}
	if _, ok := GitHubWriteVerificationFor(GitHubToolCall{Tool: "coder_edit", Mutating: true}, 0); ok {
		t.Fatal("a mutating tool outside the GitHub family must not be verified by this plan")
	}
	if _, ok := GitHubWriteVerificationCall(GitHubWriteVerification{Tool: GitHubToolPRComment}); ok {
		t.Fatal("a plan without a read tool must not render a read-back call")
	}
}

// The rendered read-back carries the write's repo and number, and takes the
// number the write returned when the call itself had none (a create).
func TestGitHubWriteVerificationCallCarriesRepoAndNumber(t *testing.T) {
	create := mutatingGitHubCalls()[0]
	plan, ok := GitHubWriteVerificationFor(create, 700)
	if !ok {
		t.Fatal("issue create must have a verification plan")
	}
	readCall, ok := GitHubWriteVerificationCall(plan)
	if !ok {
		t.Fatal("issue create must have a read-back call")
	}
	if readCall.Arguments["number"] != 700 {
		t.Fatalf("number = %v (%T), want the number the write returned",
			readCall.Arguments["number"], readCall.Arguments["number"])
	}
	if readCall.Arguments["repo"] != "msgmate-io/open-chat-go" {
		t.Fatalf("repo = %v", readCall.Arguments["repo"])
	}
	// A number already on the call is reused without a second argument.
	comment := mutatingGitHubCalls()[1]
	plan, _ = GitHubWriteVerificationFor(comment, 0)
	readCall, _ = GitHubWriteVerificationCall(plan)
	if readCall.Arguments["number"] != 514 {
		t.Fatalf("number = %v, want the call's own number", readCall.Arguments["number"])
	}
}

// An empty read-back, an error frame or one without the marker all mean NOT
// verified - the payload the write was supposed to leave is simply not there.
func TestVerifyGitHubWriteIsConservative(t *testing.T) {
	plan, ok := GitHubWriteVerificationFor(GitHubToolCall{
		Tool:      GitHubToolIssueComment,
		Mutating:  true,
		Arguments: map[string]interface{}{"repo": "msgmate-io/open-chat-go", "number": 514, "body": "noted on the umbrella"},
	}, 0)
	if !ok {
		t.Fatal("issue comment must have a verification plan")
	}
	cases := []struct {
		name     string
		readback string
	}{
		{"empty", "   \n  "},
		{"error frame", "Error: could not resolve to a repository with the name 'msgmate-io/open-chat-go'"},
		{"tool error frame", "[tool error] github_issue_view failed: 404"},
		{"json error", `{"error": "not found"}`},
		{"marker missing", "issue 514: retire coder_bash\ncomments: 3 (a different note)"},
	}
	for _, testCase := range cases {
		result := VerifyGitHubWrite(plan, testCase.readback)
		if result.Verified {
			t.Fatalf("%s: read-back must NOT verify the write", testCase.name)
		}
		if result.Weak {
			t.Fatalf("%s: an unverified write is never weakly verified", testCase.name)
		}
		if !strings.Contains(result.Reason, "NOT verified") {
			t.Fatalf("%s: reason %q must say NOT verified", testCase.name, result.Reason)
		}
		if result.Tool != GitHubToolIssueComment || result.ReadTool != GitHubToolIssueView {
			t.Fatalf("%s: verdict lost the tool pair: %+v", testCase.name, result)
		}
	}
}

// A present marker verifies the write as a genuine read, never as weak.
func TestVerifyGitHubWriteAcceptsMarkerReadback(t *testing.T) {
	plan, _ := GitHubWriteVerificationFor(GitHubToolCall{
		Tool:      GitHubToolIssueComment,
		Mutating:  true,
		Arguments: map[string]interface{}{"repo": "msgmate-io/open-chat-go", "number": 514, "body": "noted on the umbrella"},
	}, 0)
	result := VerifyGitHubWrite(plan, "issue 514: retire coder_bash\n\nnoted on the umbrella\n")
	if !result.Verified || result.Weak {
		t.Fatalf("result = %+v, want a strong verification", result)
	}
	if !strings.Contains(result.Reason, "read, not claimed") {
		t.Fatalf("reason %q must state the write was read back", result.Reason)
	}
}

// A state marker is matched case-insensitively (the API prints MERGED), while a
// body the agent wrote is matched exactly.
func TestVerifyGitHubWriteMarksStateFoldAndBodyExact(t *testing.T) {
	merge, ok := GitHubWriteVerificationFor(GitHubToolCall{
		Tool:      GitHubToolPRMerge,
		Mutating:  true,
		Arguments: map[string]interface{}{"repo": "msgmate-io/open-chat-go", "number": 873},
	}, 0)
	if !ok {
		t.Fatal("pr merge must have a verification plan")
	}
	if !merge.MarkerFold {
		t.Fatal("a state marker must be folded, the API casing differs")
	}
	if result := VerifyGitHubWrite(merge, "PR 873: state MERGED"); !result.Verified {
		t.Fatalf("MERGED must verify the merge, got %+v", result)
	}
	if result := VerifyGitHubWrite(merge, "PR 873: state OPEN"); result.Verified {
		t.Fatalf("an open PR must not verify a merge: %+v", result)
	}

	comment, _ := GitHubWriteVerificationFor(GitHubToolCall{
		Tool:      GitHubToolPRComment,
		Mutating:  true,
		Arguments: map[string]interface{}{"repo": "msgmate-io/open-chat-go", "number": 873, "body": "Ship It"},
	}, 0)
	if comment.MarkerFold {
		t.Fatal("an agent-written body must not be folded")
	}
	if result := VerifyGitHubWrite(comment, "comment: ship it"); result.Verified {
		t.Fatal("a body with a different case is not the body that was posted")
	}
}

// A review without a matched event has no marker, so the read-back proves only
// that the read succeeded: the verdict is weak and must say so instead of
// reporting a plain "verified".
func TestVerifyGitHubWriteReportsWeakVerification(t *testing.T) {
	plan, ok := GitHubWriteVerificationFor(GitHubToolCall{
		Tool:      GitHubToolPRReview,
		Mutating:  true,
		Arguments: map[string]interface{}{"repo": "msgmate-io/open-chat-go", "number": 873, "event": "REVIEW"},
	}, 0)
	if !ok {
		t.Fatal("pr review must have a verification plan")
	}
	if plan.Marker != "" {
		t.Fatalf("an unmatched review event must not invent a marker, got %q", plan.Marker)
	}
	result := VerifyGitHubWrite(plan, "PR 873: 2 reviews, 1 approved")
	if !result.Verified || !result.Weak {
		t.Fatalf("result = %+v, want a weak verification", result)
	}
	if result.Tool != GitHubToolPRReview || result.ReadTool != GitHubToolPRView {
		t.Fatalf("verdict lost the tool pair: %+v", result)
	}
	if !strings.Contains(result.Reason, "no text or state marker") {
		t.Fatalf("weak reason %q must explain why it is weak", result.Reason)
	}
	framed := FormatGitHubWriteVerification(result)
	if !strings.HasPrefix(framed, GitHubWriteVerificationPrefix) {
		t.Fatalf("frame %q must carry the verification prefix", framed)
	}
	if !strings.Contains(framed, "weakly verified") {
		t.Fatalf("frame %q must not report a weak verdict as verified", framed)
	}
	// The review *with* an event keeps the strong path.
	approve, _ := GitHubWriteVerificationFor(GitHubToolCall{
		Tool:      GitHubToolPRReview,
		Mutating:  true,
		Arguments: map[string]interface{}{"repo": "msgmate-io/open-chat-go", "number": 873, "event": "APPROVE"},
	}, 0)
	strong := VerifyGitHubWrite(approve, "review: APPROVED by reviewer")
	if !strong.Verified || strong.Weak {
		t.Fatalf("strong result = %+v, want verified and not weak", strong)
	}
	if framed := FormatGitHubWriteVerification(strong); !strings.Contains(framed, " is verified: ") {
		t.Fatalf("frame %q must report the strong verdict as verified", framed)
	}
}

// The rendered frame is what the model reads, so the unverified wording is
// pinned: it names the tool, the read-back and the missing marker.
func TestFormatGitHubWriteVerificationUnverified(t *testing.T) {
	plan, _ := GitHubWriteVerificationFor(GitHubToolCall{
		Tool:      GitHubToolIssueCreate,
		Mutating:  true,
		Arguments: map[string]interface{}{"repo": "msgmate-io/open-chat-go", "title": "a tracked issue"},
	}, 700)
	result := VerifyGitHubWrite(plan, "issue 701: something else entirely")
	framed := FormatGitHubWriteVerification(result)
	if !strings.HasPrefix(framed, GitHubWriteVerificationPrefix+GitHubToolIssueCreate+" is NOT verified: ") {
		t.Fatalf("frame = %q", framed)
	}
	if !strings.Contains(framed, `"a tracked issue"`) {
		t.Fatalf("frame %q must name the marker that was missing", framed)
	}
}
