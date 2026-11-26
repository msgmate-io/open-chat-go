package bots

import (
	"errors"
	"reflect"
	"testing"
)

// ci#1192: the nested `coder_select_workspace` selector is deep-merged, not
// replaced. A git trigger forwards only the repository it was dispatched for,
// while the bot keeps its own mode/interaction. This is the monorepo
// prerequisite for ci#1194 (a plan bot runs in the dispatched repository).
func TestApplyInteractionConfigOverridesCi1192MergesSelectorDeeply(t *testing.T) {
	effective := map[string]interface{}{
		"tool_init": map[string]interface{}{
			"coder_select_workspace": map[string]interface{}{
				"mode":         "plan",
				"interaction":  "autonomous",
				"auto_sandbox": true,
			},
			"other_tool": map[string]interface{}{"keep": true},
		},
	}
	// A git trigger forwarding the dispatched repository.
	trigger := map[string]interface{}{
		"coder_select_workspace": map[string]interface{}{
			"git_repo": "https://github.com/msgmate-io/open-chat-go.git",
		},
	}

	got := applyInteractionConfigOverrides(effective, nil, trigger)
	toolInit, ok := got["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_init missing after merge: %#v", got["tool_init"])
	}
	selector, ok := toolInit["coder_select_workspace"].(map[string]interface{})
	if !ok {
		t.Fatalf("coder_select_workspace missing after merge: %#v", toolInit)
	}
	if selector["git_repo"] != "https://github.com/msgmate-io/open-chat-go.git" {
		t.Fatalf("git_repo = %#v, want the dispatched repository", selector["git_repo"])
	}
	// The bot keeps its own mode and interaction.
	if selector["mode"] != "plan" {
		t.Fatalf("mode = %#v, want the bot default \"plan\"", selector["mode"])
	}
	if selector["interaction"] != "autonomous" {
		t.Fatalf("interaction = %#v, want the bot default \"autonomous\"", selector["interaction"])
	}
	if selector["auto_sandbox"] != true {
		t.Fatalf("auto_sandbox = %#v, want the bot default true", selector["auto_sandbox"])
	}
	if other, ok := toolInit["other_tool"].(map[string]interface{}); !ok || other["keep"] != true {
		t.Fatalf("unrelated tool_init key was dropped: %#v", toolInit)
	}
}

// An explicitly conflicting mode is a hard error, not last-writer-wins.
func TestApplyInteractionConfigOverridesCi1192ConflictingModeIsError(t *testing.T) {
	effective := map[string]interface{}{
		"tool_init": map[string]interface{}{
			"coder_select_workspace": map[string]interface{}{"mode": "plan"},
		},
	}
	override := map[string]interface{}{
		"coder_select_workspace": map[string]interface{}{"mode": "build"},
	}

	if _, err := applyInteractionConfigOverridesChecked(effective, nil, override); !errors.Is(err, errWorkspaceSelectorModeConflict) {
		t.Fatalf("checked merge error = %v, want errWorkspaceSelectorModeConflict", err)
	}
	// The legacy map-returning entry point must not silently apply the
	// conflicting mode either.
	got := applyInteractionConfigOverrides(map[string]interface{}{
		"tool_init": map[string]interface{}{
			"coder_select_workspace": map[string]interface{}{"mode": "plan"},
		},
	}, nil, override)
	selector := got["tool_init"].(map[string]interface{})["coder_select_workspace"].(map[string]interface{})
	if selector["mode"] != "plan" {
		t.Fatalf("conflicting mode won in the legacy path: %#v", selector)
	}

	// A scalar selector whose value contradicts the bot mode is refused too.
	if _, err := applyInteractionConfigOverridesChecked(effective, nil, map[string]interface{}{
		"coder_select_workspace": "build",
	}); !errors.Is(err, errWorkspaceSelectorModeConflict) {
		t.Fatalf("scalar selector error = %v, want errWorkspaceSelectorModeConflict", err)
	}

	// The same mode is not a conflict.
	if _, err := applyInteractionConfigOverridesChecked(effective, nil, map[string]interface{}{
		"coder_select_workspace": map[string]interface{}{"mode": "plan"},
	}); err != nil {
		t.Fatalf("identical mode rejected: %v", err)
	}
}

// Without a git_repo the selector stays exactly the bot default: the deep merge
// only contributes the keys the override names.
func TestApplyInteractionConfigOverridesCi1192NoRepoLeavesSelectorUnchanged(t *testing.T) {
	selector := map[string]interface{}{
		"mode":        "plan",
		"interaction": "autonomous",
		"workspace":   "bot-default-workspace",
	}
	newEffective := func() map[string]interface{} {
		return map[string]interface{}{
			"tool_init": map[string]interface{}{
				"coder_select_workspace": map[string]interface{}{
					"mode":        selector["mode"],
					"interaction": selector["interaction"],
					"workspace":   selector["workspace"],
				},
			},
		}
	}

	// No selector override at all.
	got := applyInteractionConfigOverrides(newEffective(), nil, map[string]interface{}{
		"other_tool": map[string]interface{}{"x": "y"},
	})
	merged := got["tool_init"].(map[string]interface{})["coder_select_workspace"].(map[string]interface{})
	if !reflect.DeepEqual(merged, selector) {
		t.Fatalf("selector = %#v, want the untouched bot default %#v", merged, selector)
	}

	// An override that names no git_repo must not drop the bot's own workspace
	// identity or mode either.
	got = applyInteractionConfigOverrides(newEffective(), nil, map[string]interface{}{
		"coder_select_workspace": map[string]interface{}{"workspace_name": "chat-1"},
	})
	merged = got["tool_init"].(map[string]interface{})["coder_select_workspace"].(map[string]interface{})
	if merged["mode"] != "plan" || merged["interaction"] != "autonomous" || merged["workspace"] != "bot-default-workspace" {
		t.Fatalf("bot default keys lost without a git_repo: %#v", merged)
	}
	if merged["workspace_name"] != "chat-1" {
		t.Fatalf("override key missing: %#v", merged)
	}

	// With a git_repo the stale pre-selected identity is dropped (ci#1048).
	got = applyInteractionConfigOverrides(newEffective(), nil, map[string]interface{}{
		"coder_select_workspace": map[string]interface{}{"git_repo": "https://github.com/msgmate-io/open-chat-go.git"},
	})
	merged = got["tool_init"].(map[string]interface{})["coder_select_workspace"].(map[string]interface{})
	if _, exists := merged["workspace"]; exists {
		t.Fatalf("stale workspace identity survived a git_repo override: %#v", merged)
	}
	if merged["mode"] != "plan" {
		t.Fatalf("mode = %#v, want the bot default", merged["mode"])
	}
}
