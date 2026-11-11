package tools

// github_pr_edit (ci#1035): a typed edit of an *existing* pull request.
//
// The coder could open a PR (github_pr_create), review it and merge it, but a
// follow-up fix ("retitle it", "move it onto dev", "add the test label") had to
// go through coder_bash + `gh`, which the write-verification pass cannot see.
// This tool closes that gap: it PATCHes the pull request itself, applies label
// additions/removals through the issues labels endpoints, and - like every
// typed GitHub write (ci#514 criterion 4) - re-reads the pull request and
// reports whether the edit actually landed instead of claiming it did.
//
// Two deliberate guards:
//
//   - trunk guard: the repo develops on `dev`, so a retarget away from the
//     trunk (base = main/master/anything) is refused unless the caller
//     explicitly sets allow_base_retarget. A silent retarget of an open PR is
//     exactly the kind of write that should need a human/agent decision.
//   - verified, never claimed: when the read-back does not match the request
//     the tool returns verified:false plus the concrete mismatches (it does not
//     fail the call, so the caller can retry or report the drift).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	// defaultGitHubAPIBase is the public GitHub REST API. The stub tests point
	// the tool elsewhere through init["github_api_base"].
	defaultGitHubAPIBase = "https://api.github.com"
	// githubTrunkBranch is the branch this repo develops on; a retarget away
	// from it needs allow_base_retarget.
	githubTrunkBranch = "dev"
	// githubBodyExcerpt bounds how much of an error body is echoed back.
	githubBodyExcerpt = 400
	// githubTokenInitKey is the trusted init key carrying a GitHub token for
	// the turn; the env fallbacks are the same ones the bot post-actions use.
	githubTokenInitKey = "github_token"
)

// githubTokenEnvNames is the fallback order for a GitHub token: the coder's
// post-action PAT first, then the sandbox-exported tokens.
var githubTokenEnvNames = []string{
	"OPEN_CHAT_GITHUB_POST_TOKEN",
	"GITHUB_TOKEN",
	"GH_TOKEN",
}

// GitHubPREditInput is the tool input. Every optional field that is empty is
// left untouched; at least one change is required.
type GitHubPREditInput struct {
	// Repo is "owner/name".
	Repo string `json:"repo"`
	// PullNumber is the PR number.
	PullNumber int `json:"pull_number"`

	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	// State is "open" or "closed".
	State string `json:"state,omitempty"`
	// Base retargets the PR onto another base branch; refused away from
	// githubTrunkBranch unless AllowBaseRetarget is set.
	Base string `json:"base,omitempty"`
	// AllowBaseRetarget opts into a retarget off the trunk branch.
	AllowBaseRetarget bool `json:"allow_base_retarget,omitempty"`

	AddLabels    []string `json:"add_labels,omitempty"`
	RemoveLabels []string `json:"remove_labels,omitempty"`
}

// GitHubPREditToolDef is the typed github_pr_edit tool definition.
var GitHubPREditToolDef = ToolDefinition{
	Name: "github_pr_edit",
	Description: "Edit an existing pull request: title, body, state (open/closed), base branch, and label additions/removals. " +
		"Use it instead of `gh pr edit` in coder_bash so the write is verified: the tool re-reads the pull request and reports verified:true/false with the concrete mismatches. " +
		"A base retarget away from the trunk branch `dev` is refused unless allow_base_retarget is set.",
	RequiresInit:   true,
	InputType:      GitHubPREditInput{},
	RequiredParams: []string{"repo", "pull_number"},
	Parameters: map[string]interface{}{
		"repo":                map[string]interface{}{"type": "string", "description": "Repository in owner/name form."},
		"pull_number":         map[string]interface{}{"type": "integer", "description": "Pull request number to edit."},
		"title":               map[string]interface{}{"type": "string", "description": "New title; omitted leaves it unchanged."},
		"body":                map[string]interface{}{"type": "string", "description": "New body; omitted leaves it unchanged."},
		"state":               map[string]interface{}{"type": "string", "enum": []string{"open", "closed"}, "description": "New state; omitted leaves it unchanged."},
		"base":                map[string]interface{}{"type": "string", "description": "New base branch. Away from `dev` this is refused unless allow_base_retarget is true."},
		"allow_base_retarget": map[string]interface{}{"type": "boolean", "description": "Explicitly allow a base retarget off the trunk branch `dev`."},
		"add_labels":          map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Labels to add."},
		"remove_labels":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Labels to remove."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		in, ok := input.(GitHubPREditInput)
		if !ok {
			return "", fmt.Errorf("github_pr_edit expects a GitHubPREditInput payload")
		}
		token := githubTokenFromInit(init)
		if token == "" {
			return "", fmt.Errorf("github_pr_edit needs a GitHub token: set %s in the tool init or export one of %s",
				githubTokenInitKey, strings.Join(githubTokenEnvNames, ", "))
		}
		apiBase := strings.TrimSpace(githubStringFromInit(init, "github_api_base"))
		if apiBase == "" {
			apiBase = defaultGitHubAPIBase
		}
		result, err := runGitHubPREdit(newAPIHTTPClient(), apiBase, token, in)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	},
}

// githubPREditPull is the slice of the pull payload the tool reads and verifies.
type githubPREditPull struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	Base    struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// runGitHubPREdit performs the edit, then the read-back, and returns the
// machine-readable report. Errors are reserved for a failed request or invalid
// input; a read-back that disagrees is reported as verified:false.
func runGitHubPREdit(client *http.Client, apiBase, token string, in GitHubPREditInput) (map[string]interface{}, error) {
	repo := strings.TrimSpace(in.Repo)
	if !strings.Contains(repo, "/") || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") {
		return nil, fmt.Errorf("repo must be in owner/name form, got %q", in.Repo)
	}
	if in.PullNumber <= 0 {
		return nil, fmt.Errorf("pull_number must be a positive pull request number, got %d", in.PullNumber)
	}

	patch := map[string]interface{}{}
	if title := strings.TrimSpace(in.Title); title != "" {
		patch["title"] = title
	}
	if body := strings.TrimSpace(in.Body); body != "" {
		patch["body"] = body
	}
	if state := strings.ToLower(strings.TrimSpace(in.State)); state != "" {
		if state != "open" && state != "closed" {
			return nil, fmt.Errorf("state must be \"open\" or \"closed\", got %q", in.State)
		}
		patch["state"] = state
	}
	if base := strings.TrimSpace(in.Base); base != "" {
		if base != githubTrunkBranch && !in.AllowBaseRetarget {
			return nil, fmt.Errorf("refusing to retarget %s#%d onto %q: this repo develops on %q; set allow_base_retarget=true only if a retarget off the trunk is really intended",
				repo, in.PullNumber, base, githubTrunkBranch)
		}
		patch["base"] = base
	}

	addLabels := normalizeGitHubLabels(in.AddLabels)
	removeLabels := normalizeGitHubLabels(in.RemoveLabels)
	if len(patch) == 0 && len(addLabels) == 0 && len(removeLabels) == 0 {
		return nil, fmt.Errorf("nothing to change: pass at least one of title, body, state, base, add_labels, remove_labels")
	}

	pullURL := fmt.Sprintf("%s/repos/%s/pulls/%d", strings.TrimRight(apiBase, "/"), repo, in.PullNumber)
	before, err := readGitHubPREditPull(client, pullURL, token)
	if err != nil {
		return nil, err
	}

	// A retarget onto the branch the PR already targets is not a change.
	if requestedBase, ok := patch["base"]; ok && requestedBase == before.Base.Ref {
		delete(patch, "base")
	}
	if len(patch) == 0 && len(addLabels) == 0 && len(removeLabels) == 0 {
		return nil, fmt.Errorf("nothing to change: %s#%d already targets %q", repo, in.PullNumber, before.Base.Ref)
	}

	applied := make([]string, 0, len(patch)+2)
	if len(patch) > 0 {
		if err := githubPREditRequest(client, http.MethodPatch, pullURL, token, patch, http.StatusOK); err != nil {
			return nil, err
		}
		applied = append(applied, sortedGitHubKeys(patch)...)
	}

	labelsURL := fmt.Sprintf("%s/repos/%s/issues/%d/labels", strings.TrimRight(apiBase, "/"), repo, in.PullNumber)
	if len(addLabels) > 0 {
		payload := map[string]interface{}{"labels": addLabels}
		if err := githubPREditRequest(client, http.MethodPost, labelsURL, token, payload, http.StatusOK); err != nil {
			return nil, err
		}
		applied = append(applied, "add_labels: "+strings.Join(addLabels, ","))
	}
	for _, label := range removeLabels {
		// GitHub answers 404 when the label is not on the PR; that is the
		// desired end state, so it is accepted.
		endpoint := labelsURL + "/" + url.PathEscape(label)
		if err := githubPREditRequest(client, http.MethodDelete, endpoint, token, nil, http.StatusOK, http.StatusNotFound); err != nil {
			return nil, err
		}
		applied = append(applied, "remove_labels: "+label)
	}

	after, err := readGitHubPREditPull(client, pullURL, token)
	if err != nil {
		return nil, err
	}
	verified, problems := verifyGitHubPREdit(patch, addLabels, removeLabels, after)

	requested := make([]string, 0, len(patch)+2)
	requested = append(requested, sortedGitHubKeys(patch)...)
	if len(addLabels) > 0 {
		requested = append(requested, "add_labels: "+strings.Join(addLabels, ","))
	}
	for _, label := range removeLabels {
		requested = append(requested, "remove_labels: "+label)
	}

	result := map[string]interface{}{
		"tool":         "github_pr_edit",
		"repo":         repo,
		"pull_number":  in.PullNumber,
		"requested":    requested,
		"applied":      applied,
		"verified":     verified,
		"weblink":      after.HTMLURL,
		"state_before": before.State,
		"read_back": map[string]interface{}{
			"title":  after.Title,
			"state":  after.State,
			"merged": after.Merged,
			"base":   after.Base.Ref,
			"labels": githubPREditLabelNames(after),
		},
	}
	if !verified {
		result["verification_problems"] = problems
		result["note"] = "the read-back does not show the requested edit; do not report the change as done - re-read the pull request or retry"
	}
	return result, nil
}

// verifyGitHubPREdit compares the requested patch/labels with the pull that was
// read back and returns the verdict plus a human-readable problem list.
func verifyGitHubPREdit(patch map[string]interface{}, addLabels, removeLabels []string, after githubPREditPull) (bool, []string) {
	problems := []string{}
	if want, ok := patch["title"]; ok && want != after.Title {
		problems = append(problems, fmt.Sprintf("title is %q, expected %q", after.Title, want))
	}
	if want, ok := patch["body"]; ok && want != strings.TrimSpace(after.Body) {
		problems = append(problems, "body does not match the requested body")
	}
	if want, ok := patch["state"]; ok && want != after.State {
		problems = append(problems, fmt.Sprintf("state is %q, expected %q", after.State, want))
	}
	if want, ok := patch["base"]; ok && want != after.Base.Ref {
		problems = append(problems, fmt.Sprintf("base is %q, expected %q", after.Base.Ref, want))
	}
	present := make(map[string]bool, len(after.Labels))
	for _, label := range after.Labels {
		present[strings.ToLower(strings.TrimSpace(label.Name))] = true
	}
	for _, label := range addLabels {
		if !present[strings.ToLower(label)] {
			problems = append(problems, fmt.Sprintf("label %q is missing after the edit", label))
		}
	}
	for _, label := range removeLabels {
		if present[strings.ToLower(label)] {
			problems = append(problems, fmt.Sprintf("label %q is still present after the edit", label))
		}
	}
	return len(problems) == 0, problems
}

// readGitHubPREditPull re-reads the pull request (the verification half).
func readGitHubPREditPull(client *http.Client, pullURL, token string) (githubPREditPull, error) {
	var pull githubPREditPull
	body, err := githubPREditDo(client, http.MethodGet, pullURL, token, nil, http.StatusOK)
	if err != nil {
		return pull, err
	}
	if err := json.Unmarshal(body, &pull); err != nil {
		return pull, fmt.Errorf("could not decode the pull request payload: %w", err)
	}
	return pull, nil
}

// githubPREditRequest sends one mutating request and discards the response.
func githubPREditRequest(client *http.Client, method, endpoint, token string, payload interface{}, okStatuses ...int) error {
	_, err := githubPREditDo(client, method, endpoint, token, payload, okStatuses...)
	return err
}

// githubPREditDo performs the HTTP call and returns the body for the accepted
// status codes.
func githubPREditDo(client *http.Client, method, endpoint, token string, payload interface{}, okStatuses ...int) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("could not encode the %s request body: %w", method, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("could not build the %s request: %w", method, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github %s %s failed: %w", method, redactGitHubURL(endpoint), err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, fmt.Errorf("github %s %s: could not read the response: %w", method, redactGitHubURL(endpoint), readErr)
	}
	for _, ok := range okStatuses {
		if resp.StatusCode == ok {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("github %s %s failed: %d %s", method, redactGitHubURL(endpoint), resp.StatusCode, githubBodyExcerptOf(raw))
}

// redactGitHubURL keeps error messages readable without echoing a token (the
// token travels in a header, but a query string is trimmed defensively).
func redactGitHubURL(endpoint string) string {
	if idx := strings.IndexByte(endpoint, '?'); idx >= 0 {
		return endpoint[:idx]
	}
	return endpoint
}

func githubBodyExcerptOf(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "(empty response)"
	}
	if len(text) > githubBodyExcerpt {
		return text[:githubBodyExcerpt] + "..."
	}
	return text
}

// normalizeGitHubLabels trims, de-duplicates (case-insensitively) and sorts the
// label list so the request and the report are deterministic.
func normalizeGitHubLabels(labels []string) []string {
	seen := make(map[string]bool, len(labels))
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		trimmed := strings.TrimSpace(label)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func githubPREditLabelNames(pull githubPREditPull) []string {
	names := make([]string, 0, len(pull.Labels))
	for _, label := range pull.Labels {
		names = append(names, label.Name)
	}
	sort.Strings(names)
	return names
}

func sortedGitHubKeys(patch map[string]interface{}) []string {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// githubTokenFromInit resolves the GitHub token from the trusted init map, then
// the environment. The value is never echoed.
func githubTokenFromInit(init map[string]interface{}) string {
	if token := strings.TrimSpace(githubStringFromInit(init, githubTokenInitKey)); token != "" {
		return token
	}
	for _, name := range githubTokenEnvNames {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token
		}
	}
	return ""
}

// githubStringFromInit reads a string init value, also accepting a numeric one
// (some init payloads carry numbers as float64).
func githubStringFromInit(init map[string]interface{}, key string) string {
	if init == nil {
		return ""
	}
	switch value := init[key].(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	default:
		return ""
	}
}
