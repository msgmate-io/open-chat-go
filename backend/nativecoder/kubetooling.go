package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the typed kubernetes surface of the coder_bash retirement plan
// (ci#514, phase_4_integration_clis): kubectl work becomes the typed tools
// kubernetes_get / kubernetes_describe / kubernetes_logs, owned by the
// "kubernetes" integration, mirroring the docker_sandbox surface in
// dockertooling.go.
//
// No secrets, tokens or kubeconfig data are part of this surface: it declares
// tool names, typed arguments and the shell command shapes they retire only.

// KubernetesToolPrefix is the mandatory prefix for every kubernetes tool name.
const KubernetesToolPrefix = "kubernetes_"

// KubernetesToolingVersion is bumped whenever the surface changes shape.
const KubernetesToolingVersion = 1

// KubernetesIntegration owns every typed kubernetes tool.
const KubernetesIntegration = "kubernetes"

// The typed tool names are constants so the registry and the retirement plan
// share one spelling of the surface.
const (
	// KubernetesToolGet reads resources of one kind from a cluster.
	KubernetesToolGet = "kubernetes_get"
	// KubernetesToolDescribe reads the detailed state of one resource.
	KubernetesToolDescribe = "kubernetes_describe"
	// KubernetesToolLogs returns the bounded tail of a resource's logs.
	KubernetesToolLogs = "kubernetes_logs"
)

// KubernetesToolArg is one typed argument of a kubernetes tool.
type KubernetesToolArg struct {
	Name        string
	Description string
	Required    bool
}

// KubernetesTool is one entry of the typed kubernetes surface.
type KubernetesTool struct {
	// Name is the tool name; it must carry KubernetesToolPrefix.
	Name string
	// Summary is a one-line description for the agent tool catalog.
	Summary string
	// Policy is the non-negotiable rule attached to the tool.
	Policy string
	// ReadOnly is true for inspection-only tools (all of them today).
	ReadOnly bool
	// Args are the typed arguments (never empty: untyped pass-through is what
	// this surface exists to remove).
	Args []KubernetesToolArg
	// Replaces lists the raw shell command shapes this tool retires.
	Replaces []string
}

// KubernetesToolingPlan is the snapshotable description of the kubernetes
// surface.
type KubernetesToolingPlan struct {
	Version                int
	Integration            string
	ToolPrefix             string
	SandboxRequirements    []string
	MissingClusterGuidance []string
	Tools                  []KubernetesTool
	// PendingTools are declared in the roadmap but not shipped yet.
	PendingTools []string
}

var kubernetesToolingTools = []KubernetesTool{
	{
		Name:     KubernetesToolGet,
		Summary:  "Read resources of one kind (pods, deployments, services, ...) from a cluster namespace, with structured fields.",
		Policy:   "Read-only: never create, patch, scale or delete a resource through the get tool.",
		ReadOnly: true,
		Args: []KubernetesToolArg{
			{Name: "resource", Description: "resource kind, e.g. pods or deployments", Required: true},
			{Name: "namespace", Description: "namespace to read from", Required: true},
			{Name: "name", Description: "optional single resource name", Required: false},
			{Name: "label_selector", Description: "optional label selector to filter", Required: false},
		},
		Replaces: []string{"kubectl get pods -n open-chat", "kubectl get deployments -A"},
	},
	{
		Name:     KubernetesToolDescribe,
		Summary:  "Describe one resource with its detailed state, events and conditions.",
		Policy:   "Read-only: never run a describe as a side effect of another mutation.",
		ReadOnly: true,
		Args: []KubernetesToolArg{
			{Name: "resource", Description: "resource kind, e.g. deployment", Required: true},
			{Name: "name", Description: "resource name", Required: true},
			{Name: "namespace", Description: "namespace to read from", Required: true},
		},
		Replaces: []string{"kubectl describe deployment open-chat"},
	},
	{
		Name:     KubernetesToolLogs,
		Summary:  "Return the bounded tail of a deployment or pod log, capped to keep the context bounded.",
		Policy:   "Always bound the tail; never stream logs without a line limit.",
		ReadOnly: true,
		Args: []KubernetesToolArg{
			{Name: "deployment", Description: "deployment name (or pod name)", Required: true},
			{Name: "namespace", Description: "namespace to read from", Required: true},
			{Name: "lines", Description: "number of trailing lines to return", Required: false},
		},
		Replaces: []string{"kubectl logs deploy/open-chat --tail 100"},
	},
}

var kubernetesToolingPending = []string{
	"kubernetes_exec",
	"kubernetes_port_forward",
	"kubernetes_rollout_status",
}

var kubernetesSandboxRequirements = []string{
	"kubectl or helm on PATH (never both a client and an inline cluster admin)",
	"the cluster credentials are the sandbox's own, never forwarded from a host or an agent host",
	"no implicit credentials: kubernetes tools never read or forward tokens or kubeconfig beyond the sandbox's own",
	"bounded output on every get, describe or logs call so the agent context cannot be flooded",
}

var kubernetesMissingClusterGuidance = []string{
	"When no cluster is reachable:",
	"report the gap in the PR or issue body instead of retrying kubectl commands;",
	"keep the change and run the fast, cluster-free checks (unit tests, gofmt, lint);",
	"let PR CI be the authoritative gate for deployment-shaped work.",
}

// KubernetesToolingPlanSnapshot returns a deep copy of the kubernetes surface.
func KubernetesToolingPlanSnapshot() KubernetesToolingPlan {
	plan := KubernetesToolingPlan{
		Version:                KubernetesToolingVersion,
		Integration:            KubernetesIntegration,
		ToolPrefix:             KubernetesToolPrefix,
		SandboxRequirements:    append([]string(nil), kubernetesSandboxRequirements...),
		MissingClusterGuidance: append([]string(nil), kubernetesMissingClusterGuidance...),
		PendingTools:           append([]string(nil), kubernetesToolingPending...),
		Tools:                  make([]KubernetesTool, 0, len(kubernetesToolingTools)),
	}
	for _, tool := range kubernetesToolingTools {
		plan.Tools = append(plan.Tools, cloneKubernetesTool(tool))
	}
	return plan
}

func cloneKubernetesTool(tool KubernetesTool) KubernetesTool {
	clone := tool
	clone.Args = append([]KubernetesToolArg(nil), tool.Args...)
	clone.Replaces = append([]string(nil), tool.Replaces...)
	return clone
}

// kubeShapeIsReadOnly reports whether a raw kubectl shape only inspects state.
func kubeShapeIsReadOnly(shape string) bool {
	fields := strings.Fields(strings.TrimSpace(shape))
	if len(fields) < 2 || fields[0] != "kubectl" {
		return false
	}
	switch fields[1] {
	case "get", "describe", "logs", "top", "cluster-info":
		return true
	}
	return false
}

// ValidateKubernetesToolingPlan reports every structural problem of a
// kubernetes plan.
func ValidateKubernetesToolingPlan(plan KubernetesToolingPlan) []string {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	if plan.Version != KubernetesToolingVersion {
		add("unexpected kubernetes tooling version %d (want %d)", plan.Version, KubernetesToolingVersion)
	}
	if plan.Integration != KubernetesIntegration {
		add("kubernetes tools must be owned by the %q integration, got %q", KubernetesIntegration, plan.Integration)
	}
	if plan.ToolPrefix != KubernetesToolPrefix {
		add("kubernetes tool prefix must be %q, got %q", KubernetesToolPrefix, plan.ToolPrefix)
	}
	if len(plan.SandboxRequirements) == 0 {
		add("kubernetes tooling must declare its sandbox requirements")
	}
	if len(plan.MissingClusterGuidance) == 0 {
		add("kubernetes tooling must declare what to do when no cluster is reachable")
	}
	if len(plan.Tools) == 0 {
		add("kubernetes tooling must ship at least one tool")
	}

	seen := map[string]bool{}
	for i, tool := range plan.Tools {
		switch {
		case tool.Name == "":
			add("tool %d has no name", i)
		case !strings.HasPrefix(tool.Name, KubernetesToolPrefix):
			add("tool %q is not owned by the %q integration", tool.Name, KubernetesIntegration)
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
			if !kubeShapeIsReadOnly(shape) {
				expectedReadOnly = false
				break
			}
		}
		if tool.ReadOnly != expectedReadOnly {
			add("tool %q declares read_only=%t but its retired shapes imply read_only=%t", tool.Name, tool.ReadOnly, expectedReadOnly)
		}
	}

	for _, pending := range plan.PendingTools {
		if !strings.HasPrefix(pending, KubernetesToolPrefix) {
			add("pending tool %q is not owned by the %q integration", pending, KubernetesIntegration)
		}
		if seen[pending] {
			add("tool %q is both shipped and pending", pending)
		}
	}
	return errs
}

// KubernetesToolingGaps lists declared-but-unshipped kubernetes tools, sorted.
func KubernetesToolingGaps(plan KubernetesToolingPlan) []string {
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

// RenderKubernetesToolingMarkdown renders the kubernetes surface for the docs.
func RenderKubernetesToolingMarkdown(plan KubernetesToolingPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### First-class kubernetes tooling (ci#514, phase 4)\n\n")
	fmt.Fprintf(&b, "Integration: `%s` · version %d\n\n", plan.Integration, plan.Version)
	fmt.Fprintf(&b, "Agents use the typed tools below instead of hand-written `kubectl ...` shell commands.\n\n")

	fmt.Fprintf(&b, "| tool | read-only | retires |\n")
	fmt.Fprintf(&b, "| --- | --- | --- |\n")
	for _, tool := range plan.Tools {
		fmt.Fprintf(&b, "| `%s` | %t | %s |\n", tool.Name, tool.ReadOnly, backtickJoin(tool.Replaces))
	}
	b.WriteString("\n")

	if gaps := KubernetesToolingGaps(plan); len(gaps) > 0 {
		fmt.Fprintf(&b, "Declared but not shipped yet: %s.\n\n", backtickJoin(gaps))
	}

	fmt.Fprintf(&b, "Sandbox requirements:\n")
	for _, req := range plan.SandboxRequirements {
		fmt.Fprintf(&b, "- %s\n", req)
	}
	b.WriteString("\n")

	for _, line := range plan.MissingClusterGuidance {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Per-tool policy:\n")
	for _, tool := range plan.Tools {
		fmt.Fprintf(&b, "- `%s`: %s\n", tool.Name, tool.Policy)
	}
	return b.String()
}
