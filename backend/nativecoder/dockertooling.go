package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the single source of truth for the first-class docker surface a
// coding agent gets (ci#787).
//
// Background: coding sandboxes are routinely provisioned without a docker
// daemon. Agents used to work around that by improvising raw shell commands —
// `docker compose up`, `docker run`, retrying `docker build`, occasionally even
// installing a daemon inside the sandbox. That is slow, hides the real blocker
// and can leak host state.
//
// The fix is a typed tool surface (`docker_sandbox_*`) plus one wrapper script
// that agents are told to use instead of hand-written docker shell commands.
// The registry below is what the retirement plan, the readiness probe and the
// documentation all read, so the tool names cannot drift apart.

// DockerSandboxIntegration owns every typed docker tool.
const DockerSandboxIntegration = "docker_sandbox"

// DockerToolPrefix is the mandatory prefix for every docker tool name.
const DockerToolPrefix = "docker_sandbox_"

// DockerToolingVersion is bumped whenever the surface changes shape.
const DockerToolingVersion = 1

// DockerAgentWrapper is the one supported shell entry point for docker work in
// a sandbox. It exists so an agent has an explicit, bounded fallback instead of
// improvising raw docker commands when the typed tools are not wired up yet.
const DockerAgentWrapper = "development/scripts/coder_docker.sh"

// The typed tool names are constants so the registry, the retirement plan and
// any consumer share one spelling of the surface.
const (
	// DockerToolCheck reports docker readiness in the sandbox.
	DockerToolCheck = "docker_sandbox_check"
	// DockerToolPS lists containers.
	DockerToolPS = "docker_sandbox_ps"
	// DockerToolLogs returns container or compose logs.
	DockerToolLogs = "docker_sandbox_logs"
	// DockerToolBuild builds an image from a context.
	DockerToolBuild = "docker_sandbox_build"
	// DockerToolRun runs a one-shot container.
	DockerToolRun = "docker_sandbox_run"
	// DockerToolCompose drives a compose project lifecycle.
	DockerToolCompose = "docker_sandbox_compose"
	// DockerToolExec execs in a chat-owned container.
	DockerToolExec = "docker_sandbox_exec"
)

// DockerToolArg is one typed argument of a docker tool.
type DockerToolArg struct {
	Name        string
	Description string
	Required    bool
}

// DockerTool is one entry of the typed docker surface.
type DockerTool struct {
	// Name is the tool name; it must carry DockerToolPrefix.
	Name string
	// Summary is a one-line description for the agent tool catalog.
	Summary string
	// Policy is the non-negotiable rule attached to the tool.
	Policy string
	// ReadOnly is true for inspection-only tools.
	ReadOnly bool
	// RequiresDaemon is true when the tool is useless without a reachable daemon.
	RequiresDaemon bool
	// Args are the typed arguments (never empty: untyped pass-through is what
	// this surface exists to remove).
	Args []DockerToolArg
	// Replaces lists the raw shell command shapes this tool retires.
	Replaces []string
}

// DockerToolingPlan is the snapshotable description of the docker surface.
type DockerToolingPlan struct {
	Version               int
	Integration           string
	ToolPrefix            string
	ReadinessProbe        string
	AgentWrapper          string
	SandboxRequirements   []string
	MissingDaemonGuidance []string
	Tools                 []DockerTool
	// PendingTools are declared in the roadmap but not shipped yet. They are
	// tracked explicitly so "not implemented" is a visible gap, not a surprise.
	PendingTools []string
}

var dockerToolingTools = []DockerTool{
	{
		Name:           DockerToolCheck,
		Summary:        "Report whether a docker daemon is reachable in this sandbox and what to do if not.",
		Policy:         "Run this before any other docker tool; never install or start a daemon inside the sandbox.",
		ReadOnly:       true,
		RequiresDaemon: true,
		Args: []DockerToolArg{
			{Name: "verbose", Description: "include the docker server version and any probe error", Required: false},
		},
		Replaces: []string{"docker info", "docker version"},
	},
	{
		Name:           DockerToolPS,
		Summary:        "List the containers the sandbox can see, with names, images and status.",
		Policy:         "Read-only: never stop or remove a container that the chat did not create.",
		ReadOnly:       true,
		RequiresDaemon: true,
		Args: []DockerToolArg{
			{Name: "all", Description: "include stopped containers", Required: false},
		},
		Replaces: []string{"docker ps", "docker ps -a"},
	},
	{
		Name:           DockerToolLogs,
		Summary:        "Return the tail of a container or compose service log, capped to keep the context bounded.",
		Policy:         "Always bound the tail; never stream logs without a line limit.",
		ReadOnly:       true,
		RequiresDaemon: true,
		Args: []DockerToolArg{
			{Name: "container", Description: "container or compose service name", Required: true},
			{Name: "lines", Description: "number of trailing lines to return", Required: false},
		},
		Replaces: []string{"docker logs app", "docker compose logs"},
	},
	{
		Name:           DockerToolBuild,
		Summary:        "Build an image from a context directory with a caller-provided tag.",
		Policy:         "Local builds are best-effort only: if no daemon is reachable, keep the change and let PR CI build the image.",
		ReadOnly:       false,
		RequiresDaemon: true,
		Args: []DockerToolArg{
			{Name: "tag", Description: "image tag to build", Required: true},
			{Name: "context", Description: "build context directory", Required: true},
			{Name: "args", Description: "extra docker build flags", Required: false},
		},
		Replaces: []string{"docker build -t app ."},
	},
	{
		Name:           DockerToolRun,
		Summary:        "Run a one-shot container for a check or a repro, cleaned up afterwards.",
		Policy:         "Always --rm and never --privileged; the sandbox must not accumulate containers.",
		ReadOnly:       false,
		RequiresDaemon: true,
		Args: []DockerToolArg{
			{Name: "image", Description: "image to run", Required: true},
			{Name: "command", Description: "command and arguments inside the container", Required: false},
			{Name: "volumes", Description: "explicit bind mounts, never the whole host filesystem", Required: false},
		},
		Replaces: []string{"docker run --rm app"},
	},
	{
		Name:           DockerToolCompose,
		Summary:        "Drive the lifecycle of a compose project (up, down, logs, ps) from a project directory.",
		Policy:         "Only projects inside the workspace; never touch a compose project the chat did not start.",
		ReadOnly:       false,
		RequiresDaemon: true,
		Args: []DockerToolArg{
			{Name: "project_dir", Description: "directory holding the compose file", Required: true},
			{Name: "action", Description: "one of up, down, logs, ps", Required: true},
			{Name: "service", Description: "optional compose service to target", Required: false},
		},
		Replaces: []string{"docker compose up -d", "docker compose down"},
	},
	{
		Name:           DockerToolExec,
		Summary:        "Run a command inside a container the chat owns, with a bounded output tail.",
		Policy:         "Only chat-owned containers; the command must be the exact argv, never a shell string.",
		ReadOnly:       false,
		RequiresDaemon: true,
		Args: []DockerToolArg{
			{Name: "container", Description: "chat-owned container name", Required: true},
			{Name: "command", Description: "argv to execute inside the container", Required: true},
		},
		Replaces: []string{"docker exec app ls /workspace"},
	},
}

var dockerToolingPending = []string{
	"docker_sandbox_inspect",
	"docker_sandbox_prune",
	"docker_sandbox_stats",
}

var dockerSandboxRequirements = []string{
	"docker CLI on PATH (the daemon is optional; many sandboxes have no daemon at all)",
	"no privileged mode and no host filesystem mounts outside the workspace",
	"no implicit credentials: docker tools never read or forward tokens or kubeconfig",
	"bounded output on every log or exec call so the agent context cannot be flooded",
}

var dockerMissingDaemonGuidance = []string{
	"When no daemon is reachable:",
	"report the gap in the PR or issue body instead of retrying docker commands;",
	"keep the change and run the fast, docker-free checks (unit tests, gofmt, lint);",
	"never install or start a docker daemon inside the coding sandbox;",
	"let PR CI be the authoritative gate for image builds, compose up and e2e suites.",
}

// DockerToolingPlanSnapshot returns a deep copy of the docker surface.
func DockerToolingPlanSnapshot() DockerToolingPlan {
	plan := DockerToolingPlan{
		Version:               DockerToolingVersion,
		Integration:           DockerSandboxIntegration,
		ToolPrefix:            DockerToolPrefix,
		ReadinessProbe:        DockerAgentWrapper + " check",
		AgentWrapper:          DockerAgentWrapper,
		SandboxRequirements:   append([]string(nil), dockerSandboxRequirements...),
		MissingDaemonGuidance: append([]string(nil), dockerMissingDaemonGuidance...),
		PendingTools:          append([]string(nil), dockerToolingPending...),
		Tools:                 make([]DockerTool, 0, len(dockerToolingTools)),
	}
	for _, tool := range dockerToolingTools {
		plan.Tools = append(plan.Tools, cloneDockerTool(tool))
	}
	return plan
}

func cloneDockerTool(tool DockerTool) DockerTool {
	clone := tool
	clone.Args = append([]DockerToolArg(nil), tool.Args...)
	clone.Replaces = append([]string(nil), tool.Replaces...)
	return clone
}

// readOnlyDockerShapes lists the raw docker subcommands that only inspect state.
var readOnlyDockerShapes = []string{"info", "version", "ps", "logs", "inspect", "stats", "images"}

func dockerShapeIsReadOnly(shape string) bool {
	fields := strings.Fields(strings.TrimSpace(shape))
	if len(fields) < 2 || fields[0] != "docker" {
		return false
	}
	sub := fields[1]
	if sub == "compose" && len(fields) >= 3 {
		sub = fields[2]
	}
	for _, allowed := range readOnlyDockerShapes {
		if sub == allowed {
			return true
		}
	}
	return false
}

// ValidateDockerToolingPlan reports every structural problem of a docker plan.
func ValidateDockerToolingPlan(plan DockerToolingPlan) []string {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	if plan.Version != DockerToolingVersion {
		add("unexpected docker tooling version %d (want %d)", plan.Version, DockerToolingVersion)
	}
	if plan.Integration != DockerSandboxIntegration {
		add("docker tools must be owned by the %q integration, got %q", DockerSandboxIntegration, plan.Integration)
	}
	if plan.ToolPrefix != DockerToolPrefix {
		add("docker tool prefix must be %q, got %q", DockerToolPrefix, plan.ToolPrefix)
	}
	if plan.AgentWrapper == "" {
		add("docker tooling needs an agent wrapper so agents never improvise raw docker commands")
	}
	if plan.ReadinessProbe == "" || !strings.HasPrefix(plan.ReadinessProbe, plan.AgentWrapper) {
		add("readiness probe %q must go through the agent wrapper %q", plan.ReadinessProbe, plan.AgentWrapper)
	}
	if len(plan.SandboxRequirements) == 0 {
		add("docker tooling must declare its sandbox requirements")
	}
	if len(plan.MissingDaemonGuidance) == 0 {
		add("docker tooling must declare what to do when no daemon is reachable")
	}
	if len(plan.Tools) == 0 {
		add("docker tooling must ship at least one tool")
	}

	seen := map[string]bool{}
	for i, tool := range plan.Tools {
		switch {
		case tool.Name == "":
			add("tool %d has no name", i)
		case !strings.HasPrefix(tool.Name, DockerToolPrefix):
			add("tool %q is not owned by the %q integration", tool.Name, DockerSandboxIntegration)
		}
		if seen[tool.Name] {
			add("tool %q is declared twice", tool.Name)
		}
		seen[tool.Name] = true

		if tool.Summary == "" {
			add("tool %q has no summary", tool.Name)
		}
		if tool.Policy == "" {
			add("tool %q has no policy", tool.Name)
		}
		if len(tool.Args) == 0 {
			add("tool %q must expose typed arguments", tool.Name)
		}
		if len(tool.Replaces) == 0 {
			add("tool %q does not retire any shell command shape", tool.Name)
		}
		for _, arg := range tool.Args {
			if arg.Name == "" || arg.Description == "" {
				add("tool %q has an incomplete argument declaration", tool.Name)
			}
		}

		// Read-only must follow from the retired shapes: an inspection tool
		// cannot claim to mutate, and a mutating tool cannot claim to be safe.
		expectedReadOnly := true
		for _, shape := range tool.Replaces {
			if !dockerShapeIsReadOnly(shape) {
				expectedReadOnly = false
				break
			}
		}
		if tool.ReadOnly != expectedReadOnly {
			add("tool %q declares read_only=%t but its retired shapes imply read_only=%t", tool.Name, tool.ReadOnly, expectedReadOnly)
		}
	}

	for _, pending := range plan.PendingTools {
		if !strings.HasPrefix(pending, DockerToolPrefix) {
			add("pending tool %q is not owned by the %q integration", pending, DockerSandboxIntegration)
		}
		if seen[pending] {
			add("tool %q is both shipped and pending", pending)
		}
	}
	return errs
}

// DockerToolingGaps lists declared-but-unshipped docker tools, sorted.
func DockerToolingGaps(plan DockerToolingPlan) []string {
	shipped := map[string]bool{}
	for _, tool := range plan.Tools {
		shipped[tool.Name] = true
	}
	var gaps []string
	for _, pending := range plan.PendingTools {
		if shipped[pending] {
			continue
		}
		gaps = append(gaps, pending)
	}
	sort.Strings(gaps)
	return gaps
}

// RenderDockerToolingMarkdown renders the docker surface for the docs.
func RenderDockerToolingMarkdown(plan DockerToolingPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### First-class docker tooling (ci#787)\n\n")
	fmt.Fprintf(&b, "Integration: `%s` · version %d · wrapper: `%s`\n\n", plan.Integration, plan.Version, plan.AgentWrapper)
	fmt.Fprintf(&b, "Agents use the typed tools below instead of hand-written `docker ...` shell commands.\n")
	fmt.Fprintf(&b, "The readiness probe is `%s`.\n\n", plan.ReadinessProbe)

	fmt.Fprintf(&b, "| tool | read-only | needs daemon | retires |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
	for _, tool := range plan.Tools {
		fmt.Fprintf(&b, "| `%s` | %t | %t | %s |\n", tool.Name, tool.ReadOnly, tool.RequiresDaemon, backtickJoin(tool.Replaces))
	}
	b.WriteString("\n")

	if gaps := DockerToolingGaps(plan); len(gaps) > 0 {
		fmt.Fprintf(&b, "Declared but not shipped yet: %s.\n\n", backtickJoin(gaps))
	}

	fmt.Fprintf(&b, "Sandbox requirements:\n")
	for _, req := range plan.SandboxRequirements {
		fmt.Fprintf(&b, "- %s\n", req)
	}
	b.WriteString("\n")

	for _, line := range plan.MissingDaemonGuidance {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Per-tool policy:\n")
	for _, tool := range plan.Tools {
		fmt.Fprintf(&b, "- `%s`: %s\n", tool.Name, tool.Policy)
	}
	return b.String()
}

func backtickJoin(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, "`"+value+"`")
	}
	return strings.Join(out, ", ")
}
