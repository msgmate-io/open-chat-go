package nativecoder

import "testing"

// TestHeadlessDispatchRequiresConfirmation guards ci#506/ci#622: the per-role
// native-coder selfcheck dispatches the headless roles through the trusted
// agent-dispatch surface. Such an interaction has no human watching it, so a
// bot-level `default_shared_config.require_confirmation` must not create the
// pre-start confirmation widget - nobody can approve it, so the role parks
// (`state=active finished=False`, confirmation_parking) and produces nothing.
func TestHeadlessDispatchRequiresConfirmation(t *testing.T) {
	dispatch := func(flags map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{AgentDispatchConfigKey: flags}
	}
	cases := []struct {
		name             string
		explicit         bool
		defaultGate      bool
		config           map[string]interface{}
		wantConfirmation bool
	}{
		{
			name:        "dispatch autonomous lifts the bot default",
			defaultGate: true,
			config:      dispatch(map[string]interface{}{AgentDispatchAutonomousKey: true}),
		},
		{
			name:        "approve_confirmations lifts the bot default",
			defaultGate: true,
			config:      dispatch(map[string]interface{}{AgentDispatchApproveConfirmationsKey: "true"}),
		},
		{
			name:             "explicit request still wins",
			explicit:         true,
			defaultGate:      true,
			config:           dispatch(map[string]interface{}{AgentDispatchAutonomousKey: true}),
			wantConfirmation: true,
		},
		{
			name:             "bot default honored without a dispatch",
			defaultGate:      true,
			config:           map[string]interface{}{"model": "m"},
			wantConfirmation: true,
		},
		{
			name:   "no gate configured",
			config: nil,
		},
	}
	for _, tc := range cases {
		got := HeadlessDispatchRequiresConfirmation(tc.explicit, tc.defaultGate, tc.config)
		if got != tc.wantConfirmation {
			t.Fatalf("%s: require_confirmation = %v, want %v", tc.name, got, tc.wantConfirmation)
		}
	}
}

// TestWithHeadlessDispatchNoPark is the per-action counterpart: an
// `approve_confirmations` dispatch (whose confirming user is the dispatcher, not
// a human) must also state the mode the coder integration reads, otherwise every
// mutating tool call waits for an approval that never arrives.
func TestWithHeadlessDispatchNoPark(t *testing.T) {
	approved := WithHeadlessDispatchNoPark(map[string]interface{}{
		AgentDispatchConfigKey: map[string]interface{}{AgentDispatchApproveConfirmationsKey: true},
	})
	if got := CoderInteractionFromConfig(approved); got != CoderInteractionAutonomous {
		t.Fatalf("coder_interaction = %q, want %q", got, CoderInteractionAutonomous)
	}
	if !AutonomousEnabledForConfig(approved) {
		t.Fatalf("expected autonomous policy, got %+v", ResolveAutonomousPolicy(approved))
	}

	explicit := map[string]interface{}{
		CoderInteractionConfigKey: CoderInteractionInteractive,
		AgentDispatchConfigKey:    map[string]interface{}{AgentDispatchAutonomousKey: true},
	}
	if got := WithHeadlessDispatchNoPark(explicit)[CoderInteractionConfigKey]; got != CoderInteractionInteractive {
		t.Fatalf("an explicit mode must win, got %v", got)
	}

	plain := WithHeadlessDispatchNoPark(map[string]interface{}{"model": "m"})
	if _, exists := plain[CoderInteractionConfigKey]; exists {
		t.Fatalf("a non-dispatch config must be untouched, got %v", plain)
	}
	if WithHeadlessDispatchNoPark(nil) != nil {
		t.Fatalf("nil config must stay nil")
	}
}
