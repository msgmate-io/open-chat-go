package nativecoder

import (
	"strings"
	"testing"
)

func TestValidateKubernetesToolingPlan(t *testing.T) {
	plan := KubernetesToolingPlanSnapshot()
	if errs := ValidateKubernetesToolingPlan(plan); len(errs) != 0 {
		t.Fatalf("the shipped kubernetes plan must be internally consistent, got: %v", errs)
	}
	if len(plan.Tools) == 0 {
		t.Fatal("the kubernetes plan must ship at least one tool")
	}
	for _, tool := range plan.Tools {
		if !strings.HasPrefix(tool.Name, KubernetesToolPrefix) {
			t.Fatalf("tool %q is not owned by the kubernetes integration", tool.Name)
		}
		if len(tool.Args) == 0 {
			t.Fatalf("tool %q must expose typed arguments", tool.Name)
		}
		if len(tool.Replaces) == 0 {
			t.Fatalf("tool %q does not retire any shell command shape", tool.Name)
		}
	}

	// The shipped surface is entirely read-only: every retired shape is a
	// kubectl inspection.
	for _, tool := range plan.Tools {
		if !tool.ReadOnly {
			t.Fatalf("tool %q must be read-only on the v1 surface", tool.Name)
		}
	}
}

func TestValidateKubernetesToolingPlanRejectsSquatters(t *testing.T) {
	plan := KubernetesToolingPlanSnapshot()
	plan.Tools[0].Name = "curl_cluster"
	plan.Tools[0].Replaces = []string{"kubectl delete deployment open-chat"}
	for _, err := range ValidateKubernetesToolingPlan(plan) {
		if strings.Contains(err, "not owned by") {
			return
		}
	}
	t.Fatal("a squatting tool that mutates must be rejected by validation")
}

func TestKubernetesToolingGapsListsUnshippedTools(t *testing.T) {
	plan := KubernetesToolingPlanSnapshot()
	gaps := KubernetesToolingGaps(plan)
	if len(gaps) == 0 {
		t.Fatal("the kubernetes plan must declare its not-yet-shipped tools")
	}
	for _, gap := range gaps {
		if !strings.HasPrefix(gap, KubernetesToolPrefix) {
			t.Fatalf("pending tool %q is not owned by the kubernetes integration", gap)
		}
	}
	// A shipped tool must never appear in the gaps.
	for i := 1; i < len(gaps); i++ {
		if gaps[i-1] >= gaps[i] {
			t.Fatalf("gaps must be sorted and unique, got %v", gaps)
		}
	}
	if KubernetesToolingGaps(plan) == nil {
		t.Fatal("gaps must never be nil")
	}
}

func TestRenderKubernetesToolingMarkdown(t *testing.T) {
	rendered := RenderKubernetesToolingMarkdown(KubernetesToolingPlanSnapshot())
	for _, want := range []string{
		"kubernetes_get", "kubernetes_describe", "kubernetes_logs",
		"Integration:", "Declared but not shipped yet:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered kubernetes doc must mention %q", want)
		}
	}
}

func TestKubeShapeIsReadOnly(t *testing.T) {
	for _, shape := range []string{
		"kubectl get pods -n open-chat",
		"kubectl describe deployment open-chat",
		"kubectl logs deploy/open-chat --tail 100",
	} {
		if !kubeShapeIsReadOnly(shape) {
			t.Fatalf("inspection shape %q must be read-only", shape)
		}
	}
	for _, shape := range []string{
		"kubectl delete deployment open-chat",
		"kubectl scale deploy/open-chat --replicas=3",
		"docker ps",
		"",
	} {
		if kubeShapeIsReadOnly(shape) {
			t.Fatalf("mutating or foreign shape %q must not be read-only", shape)
		}
	}
}
