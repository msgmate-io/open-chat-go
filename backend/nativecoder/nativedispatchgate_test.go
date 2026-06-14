package nativecoder

// Guards for ci#757: the legacy `trigger-open-chat-coding-interaction.yaml`
// workflow route may only be retired while (1) every workflow input is
// expressible on the native dispatch surface and (2) every observed
// development-agent chat is verified on it. The tests below pin the parity
// registry, the per-role tool profile and the gate, so a future change cannot
// quietly widen the tool list of a leaf agent or claim the retirement without
// evidence.

import (
	"encoding/json"
	"strings"
	"testing"
)

// migratedLeadChat is a chat that satisfies every axis of the gate.
func migratedLeadChat(t *testing.T) NativeDispatchChat {
	t.Helper()
	return NativeDispatchChat{
		Name:                   "lead",
		Repo:                   "msgmate-io/open-chat-go",
		Role:                   DevAgentRoleLead,
		Tools:                  mustProfile(t, DevAgentRoleLead),
		DispatchRoundTrip:      true,
		ContextRecallRoundTrip: true,
	}
}

func mustProfile(t *testing.T, role string) []string {
	t.Helper()
	tools, err := DevAgentToolProfile(role)
	if err != nil {
		t.Fatalf("DevAgentToolProfile(%q): %v", role, err)
	}
	return tools
}

func TestLegacyDispatchParityRegistryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, input := range LegacyDispatchInputParity() {
		if strings.TrimSpace(input.Name) == "" {
			t.Fatal("parity entry without a workflow input name")
		}
		if seen[input.Name] {
			t.Fatalf("duplicate parity entry for input %q", input.Name)
		}
		seen[input.Name] = true
		if strings.TrimSpace(input.Equivalent) == "" {
			// An input the native route cannot express must carry a Gap
			// instead of an empty equivalence, otherwise the retirement gate
			// would silently pass it.
			t.Fatalf("input %q has neither a native equivalent nor a gap", input.Name)
		}
	}
	// The inputs the workflow is known to accept; a drop here would hide a
	// capability regression behind a smaller registry.
	for _, required := range []string{
		"message", "bot", "tags", "approve_confirmations", "autonomous",
		"auto_continue", "max_continuations", "resume_chat_uuid", "wait",
	} {
		if !seen[required] {
			t.Errorf("parity registry lost workflow input %q", required)
		}
	}
}

func TestDevAgentToolProfileIsRoleScoped(t *testing.T) {
	lead := mustProfile(t, DevAgentRoleLead)
	for _, tool := range NativeDispatchToolFamily {
		if !containsTool(lead, tool) {
			t.Errorf("coordinating profile is missing %s", tool)
		}
	}
	for _, tool := range ChatContextRecallToolFamily {
		if !containsTool(lead, tool) {
			t.Errorf("coordinating profile is missing context recall tool %s", tool)
		}
	}

	worker := mustProfile(t, "review")
	for _, tool := range NativeDispatchToolFamily {
		if containsTool(worker, tool) {
			t.Errorf("leaf profile must not carry the dispatch tool %s", tool)
		}
	}
	for _, tool := range ChatContextRecallToolFamily {
		if !containsTool(worker, tool) {
			t.Errorf("leaf profile should still recall past chat context: missing %s", tool)
		}
	}

	// An empty role defaults to the coordinating profile (the dev-agent chat
	// the ticket is about) instead of producing an empty tool list.
	if got := mustProfile(t, "  "); len(got) != len(lead) {
		t.Errorf("empty role should default to the coordinating profile, got %d tools", len(got))
	}

	if _, err := DevAgentToolProfile("astronaut"); err == nil {
		t.Error("unknown role must be rejected instead of widening the tool list")
	}

	// The profile is a copy: mutating it must not corrupt the family.
	lead[0] = "mutated"
	if containsTool(NativeDispatchToolFamily, "mutated") || containsTool(ChatContextRecallToolFamily, "mutated") {
		t.Error("DevAgentToolProfile leaked a mutable reference to a tool family")
	}
}

func TestNativeDispatchGateBlocksUnmigratedChat(t *testing.T) {
	chat := NativeDispatchChat{
		Name:                     "lead",
		Role:                     DevAgentRoleLead,
		Tools:                    []string{"coder_bash", "github_pr_view"},
		LegacyWorkflowReferences: true,
	}
	blockers := NativeDispatchBlockers(NativeDispatchState{Chats: []NativeDispatchChat{chat}})
	joined := strings.Join(blockers, "\n")
	for _, want := range []string{
		"native dispatch family",
		NativeDispatchTool,
		"past-chat-context search family",
		"round-trip on the native route",
		LegacyDispatchWorkflowName,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected blocker mentioning %q, got:\n%s", want, joined)
		}
	}
	if NativeDispatchReady(NativeDispatchState{Chats: []NativeDispatchChat{chat}}) {
		t.Error("gate must not be ready while the chat routes through the workflow")
	}
}

func TestNativeDispatchGateReadyWhenMigrated(t *testing.T) {
	state := NativeDispatchState{Chats: []NativeDispatchChat{migratedLeadChat(t)}}
	if gaps := LegacyDispatchParityGaps(); len(gaps) > 0 {
		t.Fatalf("parity gaps must be resolved for the retirement to be ready, got %v", gaps)
	}
	if !NativeDispatchReady(state) {
		t.Fatalf("expected the gate to be ready, blockers: %v", NativeDispatchBlockers(state))
	}
	if blockers := NativeDispatchBlockers(state); len(blockers) != 0 {
		t.Fatalf("ready state must carry no blocker, got %v", blockers)
	}
}

func TestNativeDispatchGateRequiresObservedChat(t *testing.T) {
	blockers := NativeDispatchBlockers(NativeDispatchState{})
	if len(blockers) != 1 || !strings.Contains(blockers[0], "no development-agent chat was observed") {
		t.Fatalf("an empty fleet must be blocked with an explicit reason, got %v", blockers)
	}
}

func TestNativeDispatchWorkerChatNeedsNoDispatchFamily(t *testing.T) {
	chat := NativeDispatchChat{
		Name:                   "worker",
		Role:                   "review",
		Tools:                  mustProfile(t, "review"),
		ContextRecallRoundTrip: true,
	}
	if blockers := NativeDispatchChatBlockers(chat); len(blockers) != 0 {
		t.Fatalf("a migrated leaf chat must not be blocked, got %v", blockers)
	}
}

func TestLegacyDispatchReferencesDetectsGitHubRoute(t *testing.T) {
	if !LegacyDispatchReferences("then run: gh workflow run trigger-open-chat-coding-interaction.yaml") {
		t.Error("the workflow invocation must be detected")
	}
	if !LegacyDispatchReferences("see " + LegacyDispatchWorkflowName) {
		t.Error("a bare workflow file reference must be detected")
	}
	if LegacyDispatchReferences("use hive_mind_agent_dispatch to start the peer bot") {
		t.Error("the native route must not be mistaken for the legacy one")
	}
}

func TestNativeDispatchEvidenceNeverClaimsDoneWhileBlocked(t *testing.T) {
	steps := NativeDispatchEvidence(NativeDispatchState{Chats: []NativeDispatchChat{{
		Name:                     "lead",
		Role:                     DevAgentRoleLead,
		Tools:                    mustProfile(t, DevAgentRoleLead),
		DispatchRoundTrip:        true,
		ContextRecallRoundTrip:   true,
		LegacyWorkflowReferences: true,
	}}})
	byID := map[string]NativeDispatchStep{}
	for _, step := range steps {
		byID[step.ID] = step
		if step.Status != NativeDispatchStepDone && step.Status != NativeDispatchStepBlocked {
			t.Fatalf("step %s has an unknown status %q", step.ID, step.Status)
		}
		if step.Status == NativeDispatchStepBlocked && len(step.Detail) == 0 {
			t.Errorf("blocked step %s must carry its blocker lines", step.ID)
		}
	}
	if byID["native_tools_enabled"].Status != NativeDispatchStepDone {
		t.Errorf("native_tools_enabled should be done, got %+v", byID["native_tools_enabled"])
	}
	if byID["workflow_retired"].Status != NativeDispatchStepBlocked {
		t.Errorf("workflow_retired must stay blocked while a reference remains, got %+v", byID["workflow_retired"])
	}
}

func TestNativeDispatchEvidenceEmptyFleetBlocksEveryStep(t *testing.T) {
	steps := NativeDispatchEvidence(NativeDispatchState{})
	if len(steps) != len(nativeDispatchStepPlan) {
		t.Fatalf("expected one row per checklist step, got %d", len(steps))
	}
	for _, step := range steps {
		if step.Status != NativeDispatchStepBlocked {
			t.Errorf("step %s must be blocked without an observed chat, got %q", step.ID, step.Status)
		}
		if len(step.Detail) == 0 {
			t.Errorf("blocked step %s must explain why", step.ID)
		}
	}
}

func TestRenderNativeDispatchEvidenceMarkdownIsSecretFree(t *testing.T) {
	md := RenderNativeDispatchEvidenceMarkdown(NativeDispatchState{Chats: []NativeDispatchChat{migratedLeadChat(t)}})
	if !strings.Contains(md, "ci#757") || !strings.Contains(md, NativeDispatchTool) {
		t.Errorf("evidence markdown must name the ticket and the native tool, got:\n%s", md)
	}
	lower := strings.ToLower(md)
	for _, forbidden := range []string{"token", "password", "secret", "kubeconfig", "bearer"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("evidence markdown must stay credential-free, found %q", forbidden)
		}
	}
}

func TestNativeDispatchStateJSONRoundTrips(t *testing.T) {
	state := NativeDispatchState{Chats: []NativeDispatchChat{migratedLeadChat(t)}}
	raw, err := NativeDispatchStateJSON(state)
	if err != nil {
		t.Fatalf("render state: %v", err)
	}
	var back NativeDispatchState
	if err := json.Unmarshal([]byte(raw), &back); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	if len(back.Chats) != 1 || back.Chats[0].Name != "lead" || !back.Chats[0].DispatchRoundTrip {
		t.Fatalf("state did not round-trip: %s", raw)
	}
	if !NativeDispatchReady(back) {
		t.Fatalf("round-tripped migrated state must stay ready, blockers: %v", NativeDispatchBlockers(back))
	}
}

// containsTool reports whether the slice holds the tool.
func containsTool(tools []string, want string) bool {
	for _, tool := range tools {
		if tool == want {
			return true
		}
	}
	return false
}
