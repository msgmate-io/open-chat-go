package bots

import (
	"strings"
	"testing"
)

// TestBuildPlanImplementationPromptUsesHumanIssueURL guards the trigger message
// relaying the issue link to the coding agent: the clickable github.com URL must
// be used instead of the REST API URL.
func TestBuildPlanImplementationPromptUsesHumanIssueURL(t *testing.T) {
	issue := "https://api.github.com/repos/a-little-world/little-world-backend/issues/920"
	prompt := buildPlanImplementationPrompt(postActionPlanAutoImplement{
		Repo:   "a-little-world/little-world-backend",
		Number: 920,
		Issue:  &issue,
	}, "the plan")

	want := "- Issue/PR URL: https://github.com/a-little-world/little-world-backend/issues/920"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt does not contain %q:\n%s", want, prompt)
	}
	if strings.Contains(prompt, "api.github.com") {
		t.Fatalf("prompt still contains an api.github.com link:\n%s", prompt)
	}
}

// TestBuildPlanImplementationPromptKeepsHumanIssueURL keeps an already
// human-readable issue URL exactly as provided.
func TestBuildPlanImplementationPromptKeepsHumanIssueURL(t *testing.T) {
	issue := "https://github.com/a-little-world/little-world-backend/issues/920"
	prompt := buildPlanImplementationPrompt(postActionPlanAutoImplement{
		Repo:   "a-little-world/little-world-backend",
		Number: 920,
		Issue:  &issue,
	}, "the plan")

	want := "- Issue/PR URL: https://github.com/a-little-world/little-world-backend/issues/920"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt does not contain %q:\n%s", want, prompt)
	}
}

// TestBuildPlanImplementationPromptOmitsEmptyIssueURL keeps the existing
// behaviour for actions without an issue URL.
func TestBuildPlanImplementationPromptOmitsEmptyIssueURL(t *testing.T) {
	empty := "  "
	prompt := buildPlanImplementationPrompt(postActionPlanAutoImplement{
		Repo:   "acme/widgets",
		Number: 7,
		Issue:  &empty,
	}, "the plan")

	if strings.Contains(prompt, "Issue/PR URL") {
		t.Fatalf("prompt should not render an issue URL line:\n%s", prompt)
	}
}
