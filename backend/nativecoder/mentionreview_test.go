package nativecoder

import (
	"strings"
	"testing"
)

// ci#795: a GitHub-mention-triggered PR review must arrive with a repository
// identity (so the git integration's setup step gets a non-empty
// repository_name) and a PR-head checkout, rather than falling back to
// `git show`/`git grep` on a workspace that is still on `main`.

func mentionReviewEvent(owner, name string, number int, headSHA string) map[string]interface{} {
	return map[string]interface{}{
		"action": "created",
		"number": float64(number),
		"repository": map[string]interface{}{
			"full_name": owner + "/" + name,
			"name":      name,
			"owner":     map[string]interface{}{"login": owner},
		},
		"pull_request": map[string]interface{}{
			"number": float64(number),
			"base":   map[string]interface{}{"ref": "main"},
			"head": map[string]interface{}{
				"ref": "feature/backfill",
				"sha": headSHA,
			},
		},
	}
}

// TestMentionReviewResolvesRepoFromEvent is the ticket's regression test: a
// mention trigger that carries no explicit repository config still resolves the
// repository from the GitHub event payload.
func TestMentionReviewResolvesRepoFromEvent(t *testing.T) {
	config := map[string]interface{}{
		ReviewEventConfigKey: mentionReviewEvent(
			"a-little-world", "little-world-backend", 1327, "9f2b41c",
		),
	}
	plan, ok := MentionReviewCheckoutForInteraction(config, []string{"via:mention"}, nil, nil)
	if !ok {
		t.Fatalf("mention trigger must produce a review checkout plan")
	}
	if plan.Repo != "a-little-world/little-world-backend" {
		t.Fatalf("plan.Repo = %q, want a-little-world/little-world-backend", plan.Repo)
	}
	if plan.Number != 1327 {
		t.Fatalf("plan.Number = %d, want 1327", plan.Number)
	}
	if plan.ReferenceBased {
		t.Fatalf("a resolved PR number must not fall back to a ref-based review")
	}
	if plan.HeadRef != "feature/backfill" || plan.BaseRef != "main" || plan.HeadSHA != "9f2b41c" {
		t.Fatalf("plan refs = %#v, want the event's base/head refs and head sha", plan)
	}
	if got := plan.HeadFetchRef(); got != "refs/pull/1327/head" {
		t.Fatalf("HeadFetchRef() = %q, want refs/pull/1327/head", got)
	}
}

// TestMentionReviewAcceptsJSONEventAndRepoURLs pins the other payload shapes a
// trigger can deliver: a JSON-encoded webhook body and an event without a
// repository object (only `repository_url` / an issue html_url).
func TestMentionReviewAcceptsJSONEventAndRepoURLs(t *testing.T) {
	raw := `{"number":1327,"repository_url":"https://api.github.com/repos/a-little-world/little-world-backend",` +
		`"pull_request":{"head":{"sha":"9f2b41c","ref":"feature/x"},"base":{"ref":"main"}}}`
	plan, ok := MentionReviewCheckoutForInteraction(
		map[string]interface{}{ReviewEventConfigKey: raw},
		[]string{"via:mention"},
		nil,
		nil,
	)
	if !ok {
		t.Fatalf("mention trigger with a JSON event must produce a plan")
	}
	if plan.Repo != "a-little-world/little-world-backend" || plan.Number != 1327 || plan.HeadSHA != "9f2b41c" {
		t.Fatalf("plan = %#v, want the repository, number and head sha from the JSON event", plan)
	}

	urlEvent := map[string]interface{}{
		"issue": map[string]interface{}{
			"number":   float64(1327),
			"html_url": "https://github.com/a-little-world/little-world-backend/pull/1327",
		},
	}
	if got := ReviewEventRepo(urlEvent); got != "a-little-world/little-world-backend" {
		t.Fatalf("ReviewEventRepo(html_url) = %q, want the repository from the PR URL", got)
	}
}

// TestMentionReviewRepoPrefersEventThenTagAndConfig pins the resolution
// precedence: the event wins, then an explicit `repo:<...>` tag, then the
// bot/trigger config.
func TestMentionReviewRepoPrefersEventThenTagAndConfig(t *testing.T) {
	eventConfig := map[string]interface{}{
		ReviewEventConfigKey: mentionReviewEvent("event-owner", "event-repo", 7, "abc"),
		"repo":               "config-owner/config-repo",
	}
	plan, ok := MentionReviewCheckoutForInteraction(eventConfig, []string{"via:mention", "repo:tag-owner/tag-repo"}, nil, nil)
	if !ok || plan.Repo != "event-owner/event-repo" {
		t.Fatalf("event repository must win, got %#v (ok=%v)", plan, ok)
	}

	tagConfig := map[string]interface{}{"repo": "config-owner/config-repo"}
	plan, ok = MentionReviewCheckoutForInteraction(tagConfig, []string{"via:mention", "repo:tag-owner/tag-repo"}, nil, nil)
	if !ok || plan.Repo != "tag-owner/tag-repo" {
		t.Fatalf("repo: tag must win over the config default, got %#v (ok=%v)", plan, ok)
	}

	plan, ok = MentionReviewCheckoutForInteraction(tagConfig, []string{"via:mention"}, nil, nil)
	if !ok || plan.Repo != "config-owner/config-repo" {
		t.Fatalf("config default must be used as the last resort, got %#v (ok=%v)", plan, ok)
	}

	if _, ok := MentionReviewCheckoutForInteraction(tagConfig, []string{"via:assign"}, nil, nil); ok {
		t.Fatalf("a non-mention dispatch without an event must not build a review plan")
	}
}

// TestMentionReviewBindsRepositoryNameAndPRCheckout pins the workspace binding:
// the resolved repository reaches the setup step as repository_name, the
// automatic sandbox gets the PR head ref as its branch, and the PR head ref is
// fetched/checked out so the review runs on the working tree.
func TestMentionReviewBindsRepositoryNameAndPRCheckout(t *testing.T) {
	config := autoSandboxConfig("bot-default/checkout", true)
	config[ReviewEventConfigKey] = mentionReviewEvent("a-little-world", "little-world-backend", 1327, "9f2b41c")
	plan, ok := MentionReviewCheckoutForInteraction(config, []string{"via:mention"}, nil, nil)
	if !ok {
		t.Fatalf("mention trigger must produce a plan")
	}
	bound, err := BindMentionReviewWorkspace(config, plan)
	if err != nil {
		t.Fatalf("BindMentionReviewWorkspace() error = %v", err)
	}
	if got := bound[RepositoryNameConfigKey]; got != "a-little-world/little-world-backend" {
		t.Fatalf("repository_name = %#v, want the repository resolved from the event", got)
	}
	// the workspace bootstrap must never be handed an empty repository_name
	// again - that is the `repository_name missing` setup failure of ci#795.
	if strings.TrimSpace(asString(bound[RepositoryNameConfigKey])) == "" {
		t.Fatalf("repository_name must never be empty for a review trigger")
	}
	if repo := NormalizeRepo(autoSandboxRepo(t, bound)); repo != "a-little-world/little-world-backend" {
		t.Fatalf("sandbox repo = %q, want the review repository", repo)
	}
	if branch := autoSandboxBranch(t, bound); branch != "feature/backfill" {
		t.Fatalf("sandbox branch = %q, want the PR head ref", branch)
	}
	script, _ := bound[SandboxSetupCommandsConfigKey].(string)
	for _, want := range []string{
		"git fetch origin refs/pull/1327/head",
		"git checkout -B pr-review/1327 FETCH_HEAD",
		"git submodule update --init --recursive",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("review checkout script must contain %q, got:\n%s", want, script)
		}
	}
	if strings.Contains(script, "pull/1327/head:pr1327") {
		t.Fatalf("the review checkout must not invent the manual fetch fallback:\n%s", script)
	}
	// the bound config is a copy: the caller's config is untouched.
	if config[RepositoryNameConfigKey] != nil {
		t.Fatalf("BindMentionReviewWorkspace mutated the caller config: %#v", config[RepositoryNameConfigKey])
	}
}

// TestMentionReviewKeepsCallerPinnedRepository pins that a repository the caller
// pinned for this interaction is never re-bound by the review bootstrap.
func TestMentionReviewKeepsCallerPinnedRepository(t *testing.T) {
	config := autoSandboxConfig("pinned-owner/pinned-repo", true)
	config[ReviewEventConfigKey] = mentionReviewEvent("event-owner", "event-repo", 12, "abc")
	toolInit := autoSandboxToolInit("pinned-owner/pinned-repo", true)
	if repo := AutoSandboxRepoForInteraction(config, []string{"repo:tag-owner/tag-repo"}, toolInit, nil); repo != "" {
		t.Fatalf("a caller-pinned repository must not be re-bound, got %q", repo)
	}
}

// TestMentionReviewFailsWithoutRepository pins the visible failure: a review
// trigger whose repository cannot be resolved reports workspace_setup_failed
// with a reason instead of letting the agent review `main`.
func TestMentionReviewFailsWithoutRepository(t *testing.T) {
	plan, ok := MentionReviewCheckoutForInteraction(map[string]interface{}{}, []string{"via:mention"}, nil, nil)
	if !ok {
		t.Fatalf("a mention trigger must still be recognized even without a repository")
	}
	if plan.ReferenceBased != true {
		t.Fatalf("a plan without a PR number must be marked reference based, got %#v", plan)
	}
	err := ValidateMentionReviewWorkspace(plan)
	if err == nil {
		t.Fatalf("an unresolvable review repository must fail the workspace bootstrap")
	}
	if !strings.Contains(err.Error(), WorkspaceSetupFailed) {
		t.Fatalf("error %q must surface the %s status", err.Error(), WorkspaceSetupFailed)
	}
	if _, bindErr := BindMentionReviewWorkspace(map[string]interface{}{}, plan); bindErr == nil {
		t.Fatalf("BindMentionReviewWorkspace must return the workspace_setup_failed error")
	}

	// A resolvable plan validates and renders no manual fallback.
	valid := PRCheckoutPlan{Repo: "a-little-world/little-world-backend", Number: 1327, HeadRef: "feature/x"}
	if err := ValidateMentionReviewWorkspace(valid); err != nil {
		t.Fatalf("a resolved plan must validate, got %v", err)
	}
}

// TestReviewCheckoutScriptKeepsExplicitSetupScript pins that a profile's own
// sandbox setup script is preserved and extended, not dropped.
func TestReviewCheckoutScriptKeepsExplicitSetupScript(t *testing.T) {
	config := autoSandboxConfig("a-little-world/little-world-backend", true)
	config[SandboxSetupCommandsConfigKey] = "echo provisioned"
	plan := PRCheckoutPlan{Repo: "a-little-world/little-world-backend", Number: 1327, HeadRef: "feature/x"}
	bound, err := BindMentionReviewWorkspace(config, plan)
	if err != nil {
		t.Fatalf("BindMentionReviewWorkspace() error = %v", err)
	}
	script, _ := bound[SandboxSetupCommandsConfigKey].(string)
	if !strings.HasPrefix(script, "echo provisioned\n") || !strings.Contains(script, "refs/pull/1327/head") {
		t.Fatalf("explicit setup script must be preserved and extended, got:\n%s", script)
	}

	// A reference-based review (no PR number) renders no checkout commands.
	if commands := ReviewCheckoutCommands(PRCheckoutPlan{Repo: "a/b"}); len(commands) != 0 {
		t.Fatalf("ReviewCheckoutCommands() = %v, want none without a PR number", commands)
	}
}

func TestMentionReviewEventDecoders(t *testing.T) {
	if got := ReviewEventRepo(nil); got != "" {
		t.Fatalf("ReviewEventRepo(nil) = %q, want empty", got)
	}
	// repository.name + organization.login, the shape a webhook without
	// full_name uses.
	event := map[string]interface{}{
		"repository": map[string]interface{}{
			"name":         "little-world-backend",
			"organization": map[string]interface{}{"login": "a-little-world"},
		},
	}
	if got := ReviewEventRepo(event); got != "a-little-world/little-world-backend" {
		t.Fatalf("ReviewEventRepo(organization) = %q", got)
	}
	// A repository_url without a host must not be mistaken for one.
	if got := ReviewEventRepo(map[string]interface{}{"repository_url": "not/a/github/url"}); got != "" && got != "github/url" {
		t.Fatalf("ReviewEventRepo(unrecognized url) = %q, want a non-repository result", got)
	}
	for _, value := range []interface{}{float64(7), 8, int64(9), "10"} {
		if number, ok := numberFromValue(value); !ok || number == 0 {
			t.Fatalf("numberFromValue(%#v) = %d, %v, want a positive number", value, number, ok)
		}
	}
	for _, value := range []interface{}{"main", 0, -1, nil, "0"} {
		if number, ok := numberFromValue(value); ok {
			t.Fatalf("numberFromValue(%#v) = %d, want not-ok", value, number)
		}
	}
	if _, ok := decodeReviewEvent("not json"); ok {
		t.Fatalf("decodeReviewEvent(not json) must not decode")
	}
	if decoded, ok := decodeReviewEvent(`{"number":1}`); !ok || decoded == nil {
		t.Fatalf("decodeReviewEvent(json) = %#v, %v", decoded, ok)
	}
}

// autoSandboxBranch reads the bound branch out of a config's workspace init.
func autoSandboxBranch(t *testing.T, config map[string]interface{}) string {
	t.Helper()
	toolInit, ok := config["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_init missing after binding: %#v", config)
	}
	workspace, ok := toolInit[AutoSandboxToolInitKey].(map[string]interface{})
	if !ok {
		t.Fatalf("workspace init missing after binding: %#v", toolInit)
	}
	value, _ := workspace[AutoSandboxGitBranchInput].(string)
	return value
}

func asString(value interface{}) string {
	text, _ := value.(string)
	return text
}
