package nativecoder

import (
	"strings"
	"testing"
)

func TestDockerToolingPlanIsValid(t *testing.T) {
	plan := DockerToolingPlanSnapshot()
	if errs := ValidateDockerToolingPlan(plan); len(errs) != 0 {
		t.Fatalf("docker tooling plan is invalid: %v", errs)
	}
	if plan.Version != DockerToolingVersion {
		t.Fatalf("expected version %d, got %d", DockerToolingVersion, plan.Version)
	}
	if len(plan.Tools) < 6 {
		t.Fatalf("expected the docker registry to cover the common workflows, got %d tools", len(plan.Tools))
	}
}

func TestDockerToolingPlanSnapshotIsIsolated(t *testing.T) {
	first := DockerToolingPlanSnapshot()
	first.Tools[0].Name = "mutated"
	first.Tools[0].Replaces[0] = "mutated"
	first.SandboxRequirements[0] = "mutated"

	second := DockerToolingPlanSnapshot()
	if second.Tools[0].Name == "mutated" {
		t.Fatal("mutating a snapshot changed the tool name in a later snapshot")
	}
	if second.Tools[0].Replaces[0] == "mutated" {
		t.Fatal("mutating a snapshot changed the replaced command shapes in a later snapshot")
	}
	if second.SandboxRequirements[0] == "mutated" {
		t.Fatal("mutating a snapshot changed the sandbox requirements in a later snapshot")
	}
}

func TestDockerToolsRetireShellCommands(t *testing.T) {
	plan := DockerToolingPlanSnapshot()
	want := map[string]string{
		"docker ps":                     "docker_sandbox_ps",
		"docker logs app":               "docker_sandbox_logs",
		"docker build -t app .":         "docker_sandbox_build",
		"docker run --rm app":           "docker_sandbox_run",
		"docker compose up -d":          "docker_sandbox_compose",
		"docker exec app ls /workspace": "docker_sandbox_exec",
	}
	owners := map[string]string{}
	for _, tool := range plan.Tools {
		for _, replaced := range tool.Replaces {
			owners[replaced] = tool.Name
		}
	}
	for shape, tool := range want {
		if owners[shape] != tool {
			t.Fatalf("shell shape %q should be retired by %q, got %q", shape, tool, owners[shape])
		}
	}
}

func TestDockerToolsCarryIntegrationPrefixAndPolicy(t *testing.T) {
	plan := DockerToolingPlanSnapshot()
	for _, tool := range plan.Tools {
		if !strings.HasPrefix(tool.Name, DockerToolPrefix) {
			t.Fatalf("tool %q is not owned by the %q integration", tool.Name, DockerSandboxIntegration)
		}
		if tool.Policy == "" {
			t.Fatalf("tool %q has no policy", tool.Name)
		}
	}
}

func TestValidateDockerToolingPlanRejectsBadPlans(t *testing.T) {
	cases := map[string]func(plan *DockerToolingPlan){
		"missing integration": func(plan *DockerToolingPlan) { plan.Integration = "core" },
		"missing prefix":      func(plan *DockerToolingPlan) { plan.ToolPrefix = "docker_" },
		"foreign tool name":   func(plan *DockerToolingPlan) { plan.Tools[0].Name = "docker_ps" },
		"duplicate tool": func(plan *DockerToolingPlan) {
			plan.Tools = append(plan.Tools, cloneDockerTool(plan.Tools[0]))
		},
		"untyped tool":     func(plan *DockerToolingPlan) { plan.Tools[0].Args = nil },
		"policy-less tool": func(plan *DockerToolingPlan) { plan.Tools[0].Policy = "" },
		"read-only mismatch": func(plan *DockerToolingPlan) {
			plan.Tools[0].ReadOnly = false
		},
		"no guidance": func(plan *DockerToolingPlan) { plan.MissingDaemonGuidance = nil },
		"probe without wrapper": func(plan *DockerToolingPlan) {
			plan.ReadinessProbe = "docker info"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			plan := DockerToolingPlanSnapshot()
			mutate(&plan)
			if errs := ValidateDockerToolingPlan(plan); len(errs) == 0 {
				t.Fatalf("expected validation to reject the %q plan", name)
			}
		})
	}
}

func TestDockerToolingGapsListsUnshippedTools(t *testing.T) {
	plan := DockerToolingPlanSnapshot()
	gaps := DockerToolingGaps(plan)
	if len(gaps) == 0 {
		t.Fatal("expected at least one declared-but-unshipped docker tool")
	}
	for _, gap := range gaps {
		if !strings.HasPrefix(gap, DockerToolPrefix) {
			t.Fatalf("gap %q is not a docker tool", gap)
		}
	}
	for i := 1; i < len(gaps); i++ {
		if gaps[i-1] > gaps[i] {
			t.Fatalf("gaps are not sorted: %v", gaps)
		}
	}
}

func TestRenderDockerToolingMarkdown(t *testing.T) {
	rendered := RenderDockerToolingMarkdown(DockerToolingPlanSnapshot())
	for _, want := range []string{
		"First-class docker tooling (ci#787)",
		"`docker_sandbox_check`",
		"`docker_sandbox_ps`",
		DockerAgentWrapper,
		"When no daemon is reachable:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered docker tooling doc is missing %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "never install or start a docker daemon") {
		t.Fatal("rendered docker tooling doc must keep the missing-daemon guidance")
	}
}
