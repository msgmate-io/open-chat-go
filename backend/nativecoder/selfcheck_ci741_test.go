package nativecoder

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression pins for the ci#741 tracker (native-coder self-check suite,
// ci#622). The `review` role of that suite failed with
// sandbox_misprovisioned_workspace (ci#687): the role's workspace did not match
// the dispatched task, because a pre-selected monorepo checkout was handed to a
// role that had been dispatched for the task repository.
//
// The core dispatch contract the suite depends on is pinned here so it cannot
// silently regress again:
//
//  1. an explicit `repo:<owner/name>` tag on the interaction wins over the
//     bot/config default repository;
//  2. a repository the caller pinned itself in tool_init or config_overrides is
//     authoritative and is never re-bound;
//  3. otherwise the effective config's repo key is the target;
//  4. binding it surfaces the resolved repository and never mutates the
//     caller's config.
//
// A *fixed* workspace (auto_sandbox off) and any non-coder tool_init are
// deliberately left untouched by the binder, which is exactly why the
// self-check harness must dispatch such a role with the automatic sandbox and
// the task repository instead of pre-selecting a fixed monorepo workspace
// (ci#687).

const (
	ci741TaskRepo     = "msgmate-io/ci-741-fixture"
	ci741MonorepoRepo = "msgmate-io/open-chat-go"
)

// ci741ToolInit renders the auto-sandbox tool_init shape the self-check sends:
// repo empty means "the bot/integration default checkout".
func ci741ToolInit(autoSandbox bool, repo string) map[string]interface{} {
	workspace := map[string]interface{}{AutoSandboxFlagInput: autoSandbox}
	if repo != "" {
		workspace[AutoSandboxGitRepoInput] = repo
	}
	return map[string]interface{}{AutoSandboxToolInitKey: workspace}
}

// ci741BoundRepo extracts the repository a bound config clones.
func ci741BoundRepo(t *testing.T, config map[string]interface{}) string {
	t.Helper()
	toolInit, ok := config["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("bound config has no tool_init: %#v", config)
	}
	return strings.TrimSpace(AutoSandboxToolInitRepo(toolInit))
}

// TestCI741TaskRepoTagWinsOverConfigDefault is the ci#687 core: the task
// repository dispatched on the interaction is selected even though the
// effective config points at another (monorepo) checkout.
func TestCI741TaskRepoTagWinsOverConfigDefault(t *testing.T) {
	config := map[string]interface{}{"repo": ci741MonorepoRepo}
	tags := []string{"bot:coder", RepoTagPrefix + ci741TaskRepo}

	repo := AutoSandboxRepoForInteraction(config, tags, nil, nil)
	if strings.TrimSpace(repo) == "" {
		t.Fatalf("AutoSandboxRepoForInteraction = %q, want the dispatched task repo", repo)
	}
	if !strings.Contains(GitRemoteForRepo(repo), ci741TaskRepo) {
		t.Fatalf("resolved remote = %q, want it to reference %q", GitRemoteForRepo(repo), ci741TaskRepo)
	}
	if strings.Contains(GitRemoteForRepo(repo), ci741MonorepoRepo) {
		t.Fatalf("resolved remote = %q still points at the config default", GitRemoteForRepo(repo))
	}
}

// TestCI741PinnedRepoIsAuthoritative: a repository the caller pinned for this
// interaction is never overridden by a dispatched task repository.
func TestCI741PinnedRepoIsAuthoritative(t *testing.T) {
	pinned := GitRemoteForRepo(ci741TaskRepo)

	if got := ExplicitAutoSandboxRepo(ci741ToolInit(true, pinned), nil); got != pinned {
		t.Fatalf("ExplicitAutoSandboxRepo(tool_init) = %q, want %q", got, pinned)
	}
	if got := ExplicitAutoSandboxRepo(nil, map[string]interface{}{"repo": ci741TaskRepo}); got == "" {
		t.Fatal("ExplicitAutoSandboxRepo did not report the repo pinned in config_overrides")
	}
}

// TestCI741DispatchLeavesPinnedRepoAlone: with a caller pin, dispatch selects
// nothing to re-bind - the role keeps the checkout it was pinned to.
func TestCI741DispatchLeavesPinnedRepoAlone(t *testing.T) {
	tags := []string{RepoTagPrefix + ci741TaskRepo}

	if got := AutoSandboxRepoForInteraction(nil, tags, ci741ToolInit(true, ci741TaskRepo), nil); got != "" {
		t.Fatalf("pinned tool_init must disable rebinding, got %q", got)
	}
	if got := AutoSandboxRepoForInteraction(nil, tags, nil, map[string]interface{}{"repo": ci741TaskRepo}); got != "" {
		t.Fatalf("pinned config_overrides must disable rebinding, got %q", got)
	}
}

// TestCI741ConfigRepoIsTheFallbackTarget: without a tag, the effective config's
// repository is the target; an interaction that names no repository at all
// leaves the configured checkout alone.
func TestCI741ConfigRepoIsTheFallbackTarget(t *testing.T) {
	config := map[string]interface{}{"repo": ci741MonorepoRepo}
	if got := AutoSandboxRepoForInteraction(config, nil, nil, nil); !strings.Contains(got, ci741MonorepoRepo) {
		t.Fatalf("config fallback = %q, want %q", got, ci741MonorepoRepo)
	}
	// an unpinned tool_init without a repo must not block the fallback either
	if got := AutoSandboxRepoForInteraction(config, nil, ci741ToolInit(true, ""), nil); !strings.Contains(got, ci741MonorepoRepo) {
		t.Fatalf("config fallback with tool_init = %q, want %q", got, ci741MonorepoRepo)
	}
	if got := AutoSandboxRepoForInteraction(nil, nil, nil, nil); got != "" {
		t.Fatalf("empty dispatch = %q, want empty", got)
	}
}

// TestCI741BindingIsVisibleAndNonMutating: binding the resolved repository
// shows up in the bound config and leaves the caller's config untouched. A
// partially applied bind would leave the role on the wrong checkout, i.e. the
// ci#687 failure.
func TestCI741BindingIsVisibleAndNonMutating(t *testing.T) {
	config := map[string]interface{}{"tool_init": ci741ToolInit(true, "")}

	bound := WithAutoSandboxRepo(config, ci741TaskRepo)
	if got := ci741BoundRepo(t, bound); got != GitRemoteForRepo(ci741TaskRepo) {
		t.Fatalf("bound sandbox repo = %q, want %q", got, GitRemoteForRepo(ci741TaskRepo))
	}

	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal caller config: %v", err)
	}
	if strings.Contains(string(raw), ci741TaskRepo) {
		t.Fatalf("WithAutoSandboxRepo mutated the caller's config: %s", raw)
	}
}

// TestCI741FixedWorkspaceIsNeverRebound documents the other half of ci#687 from
// the core side: a fixed workspace (auto_sandbox off) and any non-coder
// tool_init keep exactly the inputs they were configured with.
func TestCI741FixedWorkspaceIsNeverRebound(t *testing.T) {
	fixed := ci741ToolInit(false, GitRemoteForRepo(ci741MonorepoRepo))
	unchanged, changed := BindAutoSandboxRepo(fixed, ci741TaskRepo)
	if changed {
		t.Fatalf("BindAutoSandboxRepo rebound a fixed workspace: %#v", unchanged)
	}
	if WithAutoSandboxRepo(map[string]interface{}{"tool_init": fixed}, ci741TaskRepo) == nil {
		t.Fatal("WithAutoSandboxRepo dropped a fixed-workspace config")
	}

	nonCoder := map[string]interface{}{"shell": map[string]interface{}{"enable": true}}
	out, changed := BindAutoSandboxRepo(nonCoder, ci741TaskRepo)
	if changed || len(out) != 1 {
		t.Fatalf("non-coder tool_init changed: changed=%v %#v", changed, out)
	}
	if got := WithAutoSandboxRepo(map[string]interface{}{"model": "selfcheck"}, ci741TaskRepo); len(got) != 1 {
		t.Fatalf("config without tool_init must be returned as-is: %#v", got)
	}
	if WithAutoSandboxRepo(nil, ci741TaskRepo) != nil {
		t.Fatal("nil config must stay nil")
	}
}

// TestCI741StringFlagEnablesBinding: interactions arrive as JSON, where the
// auto-sandbox switch may be the string "true". The string form must bind too,
// or a dispatched task repository would silently be dropped.
func TestCI741StringFlagEnablesBinding(t *testing.T) {
	toolInit := map[string]interface{}{
		AutoSandboxToolInitKey: map[string]interface{}{AutoSandboxFlagInput: "true"},
	}
	bound, changed := BindAutoSandboxRepo(toolInit, ci741TaskRepo)
	if !changed {
		t.Fatalf("BindAutoSandboxRepo ignored the string %q flag: %#v", "true", toolInit)
	}
	if got := strings.TrimSpace(AutoSandboxToolInitRepo(bound)); got != GitRemoteForRepo(ci741TaskRepo) {
		t.Fatalf("bound sandbox repo = %q, want %q", got, GitRemoteForRepo(ci741TaskRepo))
	}

	empty := map[string]interface{}{
		AutoSandboxToolInitKey: map[string]interface{}{AutoSandboxFlagInput: ""},
	}
	if _, changed := BindAutoSandboxRepo(empty, ci741TaskRepo); changed {
		t.Fatal("an empty auto_sandbox flag must not enable binding")
	}
}

// ci#927 regression pins (native-coder self-check suite, ci#622, run
// 41039710251). The `plan` role of that suite failed with
// sandbox_misprovisioned_workspace (ci#687) - the same failure mode that sank
// the `review` role in ci#741 - and the `review` role failed its own
// `review_submitted` check.
//
// The two failures are pinned here so the suite cannot silently regress again:
//
//  1. the core already wins the workspace-bind half (the ci#741 pins): a
//     dispatched task repo is bound into every auto-sandbox tool_init, but a
//     caller pin stays authoritative;
//  2. that half is a *soundness* contract that must hold not only for the
//     empty tool_init the ci#741 suite spawned, but for the tool_inits the
//     self-check actually dispatches: the harness passes an auto-sandbox
//     workspace (the tool-init-default key path), and the pinned task repo is
//     `msgmate-io/ci-927-fixture`, while the default checkout stays the
//     monorepo. The remaining gap between the ci#741 pins and a live dispatch
//     is the default-key path: with auto_sandbox only (no `git_repo` input),
//     the *pre-selected default workspace* is the monorepo and the dispatched
//     task repo must still win over it - otherwise a role is handed the wrong
//     checkout again, which is the ci#687 `plan` failure;
//  3. the review half is a delivery contract: the self-check's `review` role
//     must publish its finding as a pull request review on the fixture PR, not
//     an issue comment, so `review_submitted` can be satisfied. That contract
//     is pinned in `backend/api/bots/post_interaction_review_test.go`
//     (TestCI927ReviewPublishesAsPullRequestReview) against the same review
//     post action the suite dispatch attaches; this file carries only the
//     nativecoder-side dispatch pins.

const (
	ci927TaskRepo = "msgmate-io/ci-927-fixture"
)

// TestCI927EmptyAutoSandboxToolInitBindsTheTaskRepo is the ci#927
// tool-init-default analog of the ci#741 empty tool_init bind: the default
// auto-sandbox tool_init (the harness's exact shape, with no git repo pinned)
// carries a caller-pinned task repo into the bound config.
func TestCI927EmptyAutoSandboxToolInitBindsTheTaskRepo(t *testing.T) {
	config := map[string]interface{}{
		"tool_init": map[string]interface{}{
			AutoSandboxToolInitKey: map[string]interface{}{AutoSandboxFlagInput: true},
		},
	}

	bound := WithAutoSandboxRepo(config, ci927TaskRepo)
	if got := ci741BoundRepo(t, bound); got != GitRemoteForRepo(ci927TaskRepo) {
		t.Fatalf("bound sandbox repo = %q, want %q", got, GitRemoteForRepo(ci927TaskRepo))
	}
}

// TestCI927DispatchWinsOverTheConfigDefault if the monorepo default checkout
// wins, the role is handed the prepicked monorepo and works on - or gives up on
// - the wrong thing again, i.e. the ci#687 `plan` failure.
func TestCI927DispatchWinsOverTheConfigDefault(t *testing.T) {
	config := map[string]interface{}{"repo": ci741MonorepoRepo}

	repo := AutoSandboxRepoForInteraction(config, []string{"bot:coder", RepoTagPrefix + ci927TaskRepo}, nil, nil)
	if strings.TrimSpace(repo) == "" {
		t.Fatalf("AutoSandboxRepoForInteraction = %q, want the dispatched task repo", repo)
	}
	if !strings.Contains(GitRemoteForRepo(repo), ci927TaskRepo) {
		t.Fatalf("resolved remote = %q, want it to reference %q", GitRemoteForRepo(repo), ci927TaskRepo)
	}
	if strings.Contains(GitRemoteForRepo(repo), ci741MonorepoRepo) {
		t.Fatalf("resolved remote = %q still points at the config default", GitRemoteForRepo(repo))
	}

	// And the bind surfaces that resolution in the harness's exact tool_init
	// shape: the role's automatic sandbox clones the dispatched task repo, not
	// the pre-selected monorepo.
	bound := WithAutoSandboxRepo(config, repo)
	if got := ci741BoundRepo(t, bound); got != GitRemoteForRepo(ci927TaskRepo) {
		t.Fatalf("bound sandbox repo = %q, want %q (%q)", got, GitRemoteForRepo(ci927TaskRepo), ci927TaskRepo)
	}
}

// TestCI927SelfCheckRoleBaseConfigCarriesTheAutonomousDispatch is the
// development-lead half of the ci#927 regression: the interaction the
// self-check dispatches for the dev-lead role must run without parking on a
// per-action confirmation, otherwise a headless interaction parks on
// "Waiting for your confirmation to run coder_bash" and never produces the
// required report - the ci#506 failure mode that surfaced as the ci#622
// `dev_lead_report` self-check failure. The shield is the base config the
// suite dispatches: when it records `agent_dispatch.autonomous: true`, the
// interaction resolves autonomous and the `coder_interaction: autonomous`
// contract is promoted onto the chat's effective config.
func TestCI927SelfCheckRoleBaseConfigCarriesTheAutonomousDispatch(t *testing.T) {
	config := map[string]interface{}{
		"model": "m",
		AgentDispatchConfigKey: map[string]interface{}{
			AgentDispatchAutonomousKey: true,
		},
	}
	if !AutonomousEnabledForConfig(config) {
		t.Fatal("the self-check role dispatch must resolve autonomous (agent_dispatch.autonomous)")
	}
	if got := ResolveAutonomousPolicy(config).Source; got != AutonomousSourceDispatchAutonomous {
		t.Fatalf("autonomous source = %q, want %q", got, AutonomousSourceDispatchAutonomous)
	}
	if _, exists := config[CoderInteractionConfigKey]; exists {
		t.Fatal("the policy resolve must not mutate the caller's config")
	}
	apply := map[string]interface{}{}
	for key, value := range config {
		apply[key] = value
	}
	if got := ApplyCoderAutonomousConfig(apply); got[CoderInteractionConfigKey] != CoderInteractionAutonomous {
		t.Fatalf("apply did not promote coder_interaction: autonomous, got %#v", got)
	}
}

// TestCI927BranchDispatchReachesTheSelfCheckRole pins the second input of the
// dispatch surface the self-check harness relies on: a `branch:<ref>` tag on
// the interaction reaches the automatic sandbox checkout of the role, exactly
// like the `repo:<owner/name>` tag does. Every self-check role is dispatched
// against the same fixture PR, and its sandbox has to clone the fixture
// repository (pinned by the ci#741/ci#927 suite) at the PR's branch so the
// inherited-mode roles (plan, consolidation, dev-lead) report on the PR's
// actual content instead of the default branch.
func TestCI927BranchDispatchReachesTheSelfCheckRole(t *testing.T) {
	tags := []string{RepoTagPrefix + ci927TaskRepo, BranchTagPrefix + "selfcheck/plan"}
	if got := AutoSandboxBranchForInteraction(tags, nil); got != "selfcheck/plan" {
		t.Fatalf("AutoSandboxBranchForInteraction = %q, want the dispatched branch", got)
	}

	// The harness's exact tool_init shape is `{"coder_select_workspace":
	// {"auto_sandbox": true}}` (no repo/branch pinned); with a branch tag the
	// same bind must drop the fixture branch into the untouched repo pin.
	harnessInit := map[string]interface{}{
		AutoSandboxToolInitKey: map[string]interface{}{AutoSandboxFlagInput: true},
	}
	if bound, changed := BindAutoSandboxBranch(harnessInit, "selfcheck/plan"); !changed {
		t.Fatal("BindAutoSandboxBranch left the harness's auto-sandbox tool_init unchanged")
	} else if got := autoSandboxToolInitBranch(bound); got != "selfcheck/plan" {
		t.Fatalf("harness-shaped tool_init branch = %q, want %q", got, "selfcheck/plan")
	}
}
