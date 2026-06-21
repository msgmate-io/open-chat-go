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
