package nativecoder

import "testing"

// TestApplyCoderAutonomousConfigPromotesDispatchAutonomous guards ci#506: a
// dispatch that asked for `autonomous: true` (the ci#532 default for the native
// dev agents) must state `coder_interaction: autonomous` on the chat's own
// shared config, because that is the key the coder integration reads to lift the
// per-action confirmation gate. Without the promotion the chat only carried the
// flag inside `agent_dispatch`, so the agent parked on
// "Waiting for your confirmation to run `coder_bash`".
func TestApplyCoderAutonomousConfigPromotesDispatchAutonomous(t *testing.T) {
	config := map[string]interface{}{
		"model":   "m",
		"backend": "b",
		AgentDispatchConfigKey: map[string]interface{}{
			AgentDispatchAutonomousKey: true,
			"auto_continue":            true,
		},
	}
	applied := ApplyCoderAutonomousConfig(config)
	if got := CoderInteractionFromConfig(applied); got != CoderInteractionAutonomous {
		t.Fatalf("coder_interaction = %q, want %q", got, CoderInteractionAutonomous)
	}
	if !AutonomousEnabledForConfig(applied) {
		t.Fatalf("expected the interaction to resolve as autonomous, got %+v", ResolveAutonomousPolicy(applied))
	}
	if policy := ResolveAutonomousPolicy(applied); policy.Source != AutonomousSourceCoderInteraction {
		t.Fatalf("policy source = %q, want %q", policy.Source, AutonomousSourceCoderInteraction)
	}
}

// TestApplyCoderAutonomousConfigKeepsExplicitMode is the control: an explicit
// per-chat/per-agent mode always wins, so a bot that pins `interactive` keeps
// its per-action gate even when it is dispatched with `autonomous: true`.
func TestApplyCoderAutonomousConfigKeepsExplicitMode(t *testing.T) {
	for name, mode := range map[string]interface{}{
		"explicit interactive": CoderInteractionInteractive,
		"odd string":           "manual",
		"boolean false":        false,
	} {
		config := map[string]interface{}{
			CoderInteractionConfigKey: mode,
			AgentDispatchConfigKey: map[string]interface{}{
				AgentDispatchAutonomousKey: true,
			},
		}
		applied := ApplyCoderAutonomousConfig(config)
		if got := applied[CoderInteractionConfigKey]; got != mode {
			t.Fatalf("%s: coder_interaction = %v, want the explicit %v", name, got, mode)
		}
		if CoderInteractionAutonomousForConfig(applied) {
			t.Fatalf("%s: an explicit non-autonomous mode must keep the per-action gate", name)
		}
	}
}

// TestApplyCoderAutonomousConfigIgnoresNonAutonomousDispatch pins the safety
// boundary: only an explicit `autonomous: true` request changes the mode. A
// dispatch without it keeps the per-action gate, `approve_confirmations` is not
// promoted (its confirming user stands in for the agent, so the interaction may
// still ask — the integration keeps that flag's narrower semantics), and a
// non-dispatch config is never touched.
func TestApplyCoderAutonomousConfigIgnoresNonAutonomousDispatch(t *testing.T) {
	config := map[string]interface{}{
		"model": "m",
		AgentDispatchConfigKey: map[string]interface{}{
			"auto_continue":                      true,
			AgentDispatchApproveConfirmationsKey: true,
			AgentDispatchAutonomousKey:           false,
		},
	}
	applied := ApplyCoderAutonomousConfig(config)
	if _, present := applied[CoderInteractionConfigKey]; present {
		t.Fatalf("coder_interaction must not be added without an explicit autonomous request, got %v", applied)
	}
	if CoderInteractionAutonomousForConfig(applied) {
		t.Fatalf("the chat mode must not be promoted from approve_confirmations alone, got %v", applied)
	}
	// The flag is still *reported* as a no-park interaction so a watching agent
	// can tell the dispatcher stands in as the confirming user.
	if policy := ResolveAutonomousPolicy(applied); !policy.Enabled || policy.Source != AutonomousSourceDispatchApproveConfirmations {
		t.Fatalf("approve_confirmations policy = %+v, want enabled from %q", policy, AutonomousSourceDispatchApproveConfirmations)
	}
	if AutonomousEnabledForConfig(map[string]interface{}{"model": "m", "backend": "b"}) {
		t.Fatal("a plain assistant config must never resolve as autonomous")
	}
	if AutonomousEnabledForConfig(nil) {
		t.Fatal("a nil config must never resolve as autonomous")
	}
}
