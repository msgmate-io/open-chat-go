package msgmate

import (
	"reflect"
	"strings"
	"testing"
)

func TestRoleGrantForNormalizesName(t *testing.T) {
	for _, name := range []string{"plan", "Plan", "  PLAN "} {
		grant, ok := RoleGrantFor(name)
		if !ok {
			t.Fatalf("RoleGrantFor(%q) reported unknown role", name)
		}
		if grant.Role != RolePlan {
			t.Fatalf("RoleGrantFor(%q).Role = %q, want %q", name, grant.Role, RolePlan)
		}
	}
	for _, name := range []string{"", "   ", "does-not-exist"} {
		if _, ok := RoleGrantFor(name); ok {
			t.Fatalf("RoleGrantFor(%q) should report an unknown role", name)
		}
	}
}

func TestRequiredRoleToolsPlan(t *testing.T) {
	want := []string{
		"github_issue_view",
		"github_issue_list",
		"github_pr_view",
		"github_file_read",
		"github_run_view",
		"github_issue_comment",
		"coder_dispatch_agent",
	}
	if got := RequiredRoleTools(RolePlan); !reflect.DeepEqual(got, want) {
		t.Fatalf("RequiredRoleTools(plan) = %v, want %v", got, want)
	}
	if got := RequiredRoleTools("nope"); got != nil {
		t.Fatalf("RequiredRoleTools(unknown) = %v, want nil", got)
	}
}

func TestMissingRoleGrants(t *testing.T) {
	missing := MissingRoleGrants(RolePlan, []string{"github_issue_view", "coder_dispatch_agent"})
	want := []string{
		"github_issue_list",
		"github_pr_view",
		"github_file_read",
		"github_run_view",
		"github_issue_comment",
	}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("MissingRoleGrants = %v, want %v", missing, want)
	}
	if got := MissingRoleGrants("nope", nil); got != nil {
		t.Fatalf("MissingRoleGrants(unknown) = %v, want nil", got)
	}
	// A complete selection has no gaps.
	if got := MissingRoleGrants(RolePlan, RequiredRoleTools(RolePlan)); len(got) != 0 {
		t.Fatalf("MissingRoleGrants(complete) = %v, want empty", got)
	}
}

func TestMergeRoleGrantsIsAdditive(t *testing.T) {
	configured := []string{"coder_read", "gITHUB_Issue_View", "github_pr_edit"}
	toolInit := map[string]interface{}{"github_issue_comment": map[string]interface{}{"repo": "msgmate-io/open-chat-go"}}

	tools, init := MergeRoleGrants(RolePlan, configured, toolInit)

	// Configured tools keep their order and are never removed.
	if got := tools[:len(configured)]; !reflect.DeepEqual(got, configured) {
		t.Fatalf("configured prefix = %v, want %v", got, configured)
	}
	// Every grant is present exactly once, case-insensitively.
	seen := map[string]int{}
	for _, tool := range tools {
		seen[strings.ToLower(tool)]++
	}
	for _, required := range RequiredRoleTools(RolePlan) {
		if seen[strings.ToLower(required)] != 1 {
			t.Fatalf("tool %q appears %d times in %v", required, seen[strings.ToLower(required)], tools)
		}
	}
	if len(tools) != len(configured)+len(RequiredRoleTools(RolePlan))-1 {
		// github_issue_view was already configured, so only the other six grants are added.
		t.Fatalf("merged length = %d, want %d (%v)", len(tools), len(configured)+len(RequiredRoleTools(RolePlan))-1, tools)
	}
	if !reflect.DeepEqual(init, toolInit) {
		t.Fatalf("toolInit = %v, want %v", init, toolInit)
	}
	if len(init) != 1 {
		t.Fatalf("toolInit = %v, want a single entry", init)
	}

	// The inputs are not mutated.
	if len(configured) != 3 || configured[1] != "gITHUB_Issue_View" {
		t.Fatalf("input slice was mutated: %v", configured)
	}
}

func TestMergeRoleGrantsIsIdempotent(t *testing.T) {
	toolInit := map[string]interface{}{}
	first, _ := MergeRoleGrants(RolePlan, []string{"coder_read"}, toolInit)
	second, _ := MergeRoleGrants(RolePlan, first, toolInit)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("merge is not idempotent: %v then %v", first, second)
	}
}

func TestMergeRoleGrantsUnknownRoleIsNoOp(t *testing.T) {
	configured := []string{"coder_read"}
	for _, role := range []string{"", "  ", "nope"} {
		tools, init := MergeRoleGrants(role, configured, nil)
		if !reflect.DeepEqual(tools, configured) {
			t.Fatalf("MergeRoleGrants(%q) tools = %v, want %v", role, tools, configured)
		}
		if init == nil || len(init) != 0 {
			t.Fatalf("MergeRoleGrants(%q) toolInit = %v, want empty map", role, init)
		}
	}
}
