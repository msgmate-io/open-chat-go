package nativecoder

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDefaultRecoveryProfileValid asserts the shipped ci#658 profile satisfies
// every invariant of ValidateRecoveryProfile (not just that the validator
// exists).
func TestDefaultRecoveryProfileValid(t *testing.T) {
	profile := DefaultRecoveryAgentProfile()
	if errs := ValidateRecoveryProfile(profile); len(errs) > 0 {
		t.Fatalf("default recovery profile is invalid: %v", errs)
	}
	if profile.Name != RecoveryAgentName || profile.Target.Name != RecoveryAgentName {
		t.Errorf("profile is not for the %s deployment: %+v", RecoveryAgentName, profile.Target)
	}
	if profile.Target.Host != RecoveryAgentHost {
		t.Errorf("expected the new deployment host %s, got %q", RecoveryAgentHost, profile.Target.Host)
	}
	if len(profile.Target.Monitors) != 1 || profile.Target.Monitors[0] != MonitoredHost {
		t.Errorf("expected the profile to watch exactly %s, got %v", MonitoredHost, profile.Target.Monitors)
	}
	if profile.Role != RecoveryAgentRole || !containsString(knownRoles, profile.Role) {
		t.Errorf("recovery role %q is not a known native coder role", profile.Role)
	}
}

// TestRecoveryProfileCoversEveryCapability asserts the restricted subset really
// can monitor, diagnose, recover and report - the three (plus reporting) things
// the ticket asks the agent to be capable of.
func TestRecoveryProfileCoversEveryCapability(t *testing.T) {
	profile := DefaultRecoveryAgentProfile()
	covered := map[string]bool{}
	for _, probe := range profile.Probes {
		covered[probe.Capability] = true
	}
	for _, capability := range RecoveryCapabilities {
		if !covered[capability] {
			t.Errorf("capability %q has no probe in the default profile", capability)
		}
	}
	// Losing a class must be a validation error, otherwise the coverage
	// requirement is decorative.
	drop := DefaultRecoveryAgentProfile()
	kept := []RecoveryProbe{}
	for _, probe := range drop.Probes {
		if probe.Capability == RecoveryCapabilityRecover {
			continue
		}
		kept = append(kept, probe)
	}
	drop.Probes = kept
	if errs := ValidateRecoveryProfile(drop); len(errs) == 0 {
		t.Error("validator accepted a profile that cannot recover anything")
	}
}

// TestRecoveryProfileEveryCapabilityIsCarriedByAScenario asserts the capability
// plan's emergency_recovery class is backed by a real scenario that tracks
// ci#658, so the new deployment's agent stays covered by the capability suite.
func TestRecoveryProfileEveryCapabilityIsCarriedByAScenario(t *testing.T) {
	scenario, ok := ScenarioByID("emergency_recovery_watchdog")
	if !ok {
		t.Fatal("expected the emergency_recovery_watchdog scenario to exist")
	}
	if !containsInt(scenario.Tracks, 658) {
		t.Errorf("scenario does not track ci#658: %v", scenario.Tracks)
	}
	if scenario.Role != RecoveryAgentRole {
		t.Errorf("scenario role %q is not the recovery role %q", scenario.Role, RecoveryAgentRole)
	}
	if coverage := CapabilityCoverage(); len(coverage["emergency_recovery"]) == 0 {
		t.Error("emergency_recovery is not covered by any scenario")
	}
	for _, tool := range scenario.Tools {
		if !isKnownRecoveryTool(tool) {
			t.Errorf("scenario tool %q is outside the restricted recovery subset", tool)
		}
	}
	profile := DefaultRecoveryAgentProfile()
	for _, tool := range scenario.Tools {
		if !containsString(profile.Tools, tool) {
			t.Errorf("scenario tool %q is not in the recovery profile tool subset %v", tool, profile.Tools)
		}
	}
}

// TestValidateRecoveryProfileRejectsBrokenProfiles asserts the validator fails
// on each class of defect a hand-edited deployment config could introduce.
func TestValidateRecoveryProfileRejectsBrokenProfiles(t *testing.T) {
	base := DefaultRecoveryAgentProfile()
	if errs := ValidateRecoveryProfile(base); len(errs) != 0 {
		t.Fatalf("base profile must be valid, got %v", errs)
	}

	production := DefaultRecoveryAgentProfile()
	production.Target.Host = ProductionHost
	production.Target.Monitors = []string{ProductionHost}
	if len(ValidateRecoveryProfile(production)) == 0 {
		t.Error("validator accepted production as a recovery target")
	}

	mismatch := DefaultRecoveryAgentProfile()
	mismatch.Target.Name = "somewhere-else"
	if len(ValidateRecoveryProfile(mismatch)) == 0 {
		t.Error("validator accepted a target name that does not match the profile")
	}

	noCredential := DefaultRecoveryAgentProfile()
	noCredential.Target.CredentialRef = "  "
	if len(ValidateRecoveryProfile(noCredential)) == 0 {
		t.Error("validator accepted a profile with no credential reference")
	}

	secret := DefaultRecoveryAgentProfile()
	secret.Target.CredentialRef = "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if len(ValidateRecoveryProfile(secret)) == 0 {
		t.Error("validator accepted a credential reference that embeds a secret")
	}

	secretProbe := DefaultRecoveryAgentProfile()
	secretProbe.Probes[0].Command = "curl -H 'Authorization: Bearer sk-live-secret' " + MonitoredHost
	if len(ValidateRecoveryProfile(secretProbe)) == 0 {
		t.Error("validator accepted a probe command carrying a secret")
	}

	noTrigger := DefaultRecoveryAgentProfile()
	noTrigger.Triggers = nil
	if len(ValidateRecoveryProfile(noTrigger)) == 0 {
		t.Error("validator accepted a profile with no trigger")
	}

	unknownTrigger := DefaultRecoveryAgentProfile()
	unknownTrigger.Triggers = []string{"whenever"}
	if len(ValidateRecoveryProfile(unknownTrigger)) == 0 {
		t.Error("validator accepted an unknown trigger")
	}

	wideTool := DefaultRecoveryAgentProfile()
	wideTool.Tools = append(wideTool.Tools, "coder_select_workspace_provision")
	wideTool.Tools = append(wideTool.Tools, "mcp:filesystem")
	if errs := ValidateRecoveryProfile(wideTool); len(errs) == 0 {
		t.Error("validator accepted tools outside the restricted recovery subset")
	}

	ungated := DefaultRecoveryAgentProfile()
	for i := range ungated.Probes {
		if ungated.Probes[i].Mutating {
			ungated.Probes[i].RequiresConfirmation = false
		}
	}
	if len(ValidateRecoveryProfile(ungated)) == 0 {
		t.Error("validator accepted an ungated mutating probe")
	}

	noAttempts := DefaultRecoveryAgentProfile()
	noAttempts.MaxRecoveryAttempts = 0
	if len(ValidateRecoveryProfile(noAttempts)) == 0 {
		t.Error("validator accepted an unbounded recovery attempt budget")
	}

	badRole := DefaultRecoveryAgentProfile()
	badRole.Role = "wizard"
	if len(ValidateRecoveryProfile(badRole)) == 0 {
		t.Error("validator accepted an unknown native coder role")
	}

	unknownCapability := DefaultRecoveryAgentProfile()
	unknownCapability.Probes[0].Capability = "guess"
	if len(ValidateRecoveryProfile(unknownCapability)) == 0 {
		t.Error("validator accepted an unknown recovery capability")
	}
}

// TestRecoveryProfileSubsetIsNarrow asserts the recovery bot is a strict subset
// of the full coder surface: it holds no sandbox, web, MCP or sub-agent tool.
func TestRecoveryProfileSubsetIsNarrow(t *testing.T) {
	profile := DefaultRecoveryAgentProfile()
	forbidden := []string{
		"coder_select_workspace", "coder_webfetch", "coder_websearch", "coder_task",
		"mcp:", "hive_mind_agent_dispatch",
	}
	for _, tool := range profile.Tools {
		for _, banned := range forbidden {
			if tool == banned || strings.HasPrefix(tool, banned) {
				t.Errorf("recovery profile must not hold %q", tool)
			}
		}
		if !isKnownRecoveryTool(tool) {
			t.Errorf("recovery tool %q is outside the allowed families", tool)
		}
	}
	if len(profile.Tools) > 8 {
		t.Errorf("recovery subset grew to %d tools; keep it small", len(profile.Tools))
	}
	gated := MutatingProbes(profile)
	if len(gated) != 1 || gated[0].ID != "service_restart" {
		t.Errorf("expected exactly the confirmation-gated restart probe, got %v", gated)
	}
}

// TestRecoveryPromptCarriesTriggerAndLimits asserts the generated task message
// names the trigger, the watched host and production's prohibition, and carries
// the marker token so the harness can assert a fresh nonce.
func TestRecoveryPromptCarriesTriggerAndLimits(t *testing.T) {
	prompt := RecoveryPrompt(DefaultRecoveryAgentProfile(), TriggerMonitoringCrash)
	for _, want := range []string{TriggerMonitoringCrash, MonitoredHost, RecoveryAgentHost, ProductionHost, recoveryMarkerToken} {
		if !strings.Contains(prompt, want) {
			t.Errorf("recovery prompt does not mention %q", want)
		}
	}
	if RecoveryPrompt(DefaultRecoveryAgentProfile(), TriggerPreDeploy) == prompt {
		t.Error("expected the pre-deploy prompt to differ from the monitoring-crash prompt")
	}
}

// TestRecoveryProfileJSONStable asserts the descriptor round-trips through JSON
// with the documented keys, so the private deployment tooling can consume the
// same shape.
func TestRecoveryProfileJSONStable(t *testing.T) {
	raw, err := json.Marshal(DefaultRecoveryAgentProfile())
	if err != nil {
		t.Fatalf("marshal recovery profile: %v", err)
	}
	for _, key := range []string{"\"name\"", "\"role\"", "\"target\"", "\"monitors\"", "\"credential_ref\"", "\"triggers\"", "\"tools\"", "\"probes\"", "\"requires_confirmation\"", "\"max_recovery_attempts\""} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("recovery profile JSON is missing %s", key)
		}
	}
	roundTrip := RecoveryAgentProfile{}
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal recovery profile: %v", err)
	}
	if errs := ValidateRecoveryProfile(roundTrip); len(errs) != 0 {
		t.Errorf("round-tripped profile is invalid: %v", errs)
	}
	if len(roundTrip.Probes) != len(DefaultRecoveryAgentProfile().Probes) {
		t.Errorf("probe count changed across the round trip: %d", len(roundTrip.Probes))
	}
}
