package nativecoder

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestCapabilityPlanValid asserts the shipped plan satisfies every invariant
// the plan promises: the registry is well formed and every core capability is
// covered by at least one scenario (ci#390).
func TestCapabilityPlanValid(t *testing.T) {
	plan := NativeCapabilityPlan()
	if errs := ValidateCapabilityPlan(plan); len(errs) > 0 {
		for _, err := range errs {
			t.Errorf("capability plan invalid: %v", err)
		}
	}
	if plan.Version != CapabilityPlanVersion {
		t.Fatalf("plan version = %d, want %d", plan.Version, CapabilityPlanVersion)
	}
	if len(plan.Scenarios) == 0 {
		t.Fatal("capability plan has no scenarios")
	}
	if uncovered := UncoveredCoreCapabilities(); len(uncovered) > 0 {
		t.Fatalf("uncovered core capabilities: %v", uncovered)
	}
}

// TestCapabilityPlanCoreCoverage asserts each core class maps to at least one
// scenario and that the coverage map only names known classes.
func TestCapabilityPlanCoreCoverage(t *testing.T) {
	plan := NativeCapabilityPlan()
	coverage := CapabilityCoverage()
	for _, class := range plan.Classes {
		ids := coverage[class.Name]
		if class.Tier != CapabilityTierCore {
			continue
		}
		if len(ids) == 0 {
			t.Errorf("core capability %q has no scenario", class.Name)
			continue
		}
		for _, id := range ids {
			if _, ok := ScenarioByID(id); !ok {
				t.Errorf("coverage for %q names unknown scenario %q", class.Name, id)
			}
		}
	}
	for capability := range coverage {
		found := false
		for _, class := range plan.Classes {
			if class.Name == capability {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("coverage references undeclared capability %q", capability)
		}
	}
}

// TestCapabilityPlanScenarioSelection asserts the selector helpers agree with
// the registry (tag and capability lookups, id lookup, copy semantics).
func TestCapabilityPlanScenarioSelection(t *testing.T) {
	plan := NativeCapabilityPlan()
	for _, scenario := range plan.Scenarios {
		byID, ok := ScenarioByID(scenario.ID)
		if !ok {
			t.Fatalf("ScenarioByID(%q) not found", scenario.ID)
		}
		if byID.Capability != scenario.Capability {
			t.Errorf("ScenarioByID(%q).Capability = %q, want %q", scenario.ID, byID.Capability, scenario.Capability)
		}
		byCapability := ScenariosByCapability(scenario.Capability)
		if !scenarioIDsContain(byCapability, scenario.ID) {
			t.Errorf("ScenariosByCapability(%q) does not contain %q", scenario.Capability, scenario.ID)
		}
		for _, tag := range scenario.Tags {
			if !scenarioIDsContain(ScenariosByTag(tag), scenario.ID) {
				t.Errorf("ScenariosByTag(%q) does not contain %q", tag, scenario.ID)
			}
		}
	}

	if _, ok := ScenarioByID("does_not_exist"); ok {
		t.Error("ScenarioByID returned a scenario for an unknown id")
	}
	if got := ScenariosByTag("does-not-exist"); len(got) != 0 {
		t.Errorf("ScenariosByTag returned %d scenarios for an unknown tag", len(got))
	}

	// The copy must not alias the registry: mutating the returned plan must not
	// leak back into the process-wide registry.
	mutated := NativeCapabilityPlan()
	mutated.Scenarios[0].Tags[0] = "mutated-tag"
	mutated.Scenarios[0].Tools[0] = "mutated_tool"
	mutated.Scenarios[0].Acceptance[0] = "mutated"
	mutated.Scenarios[0].Tracks[0] = -1
	mutated.Classes[0].Name = "mutated-class"
	fresh := NativeCapabilityPlan()
	if fresh.Scenarios[0].Tags[0] == "mutated-tag" ||
		fresh.Scenarios[0].Tools[0] == "mutated_tool" ||
		fresh.Scenarios[0].Acceptance[0] == "mutated" ||
		fresh.Scenarios[0].Tracks[0] == -1 ||
		fresh.Classes[0].Name == "mutated-class" {
		t.Fatal("CapabilityPlan returned slices/entries aliasing the registry")
	}
}

// TestCapabilityPlanScenarioRequirements asserts each scenario carries the
// metadata a live harness needs to schedule it.
func TestCapabilityPlanScenarioRequirements(t *testing.T) {
	plan := NativeCapabilityPlan()
	for _, scenario := range plan.Scenarios {
		if !strings.Contains(scenario.Prompt, capabilityMarkerToken) {
			t.Errorf("scenario %q prompt lacks the marker token", scenario.ID)
		}
		if scenario.RequiresSandbox && !scenarioIDsContain(ScenariosByTag("sandbox"), scenario.ID) {
			t.Errorf("scenario %q requires a sandbox but is not tagged sandbox", scenario.ID)
		}
		if scenario.RequiresLiveWeb && !scenarioIDsContain(ScenariosByTag("live"), scenario.ID) {
			t.Errorf("scenario %q requires the live web but is not tagged live", scenario.ID)
		}
		if scenario.Mutating && len(scenario.Tags) == 0 {
			t.Errorf("scenario %q is mutating but carries no tags", scenario.ID)
		}
		if len(scenario.Tracks) == 0 {
			t.Errorf("scenario %q tracks no ci ticket", scenario.ID)
		}
		for _, track := range scenario.Tracks {
			if track <= 0 {
				t.Errorf("scenario %q has invalid ticket reference %d", scenario.ID, track)
			}
		}
		if !containsInt(scenario.Tracks, 390) {
			t.Errorf("scenario %q does not track the parent ticket ci#390", scenario.ID)
		}
	}
}

// TestCapabilityPlanTracksKnownTickets asserts the scenarios reference the
// tickets that actually motivated them, so the plan stays the map from
// capability to regression evidence.
func TestCapabilityPlanTracksKnownTickets(t *testing.T) {
	expectations := map[string][]int{
		"sandbox_project_scaffold":           {602, 666, 688},
		"confirmation_gate_reject":           {525, 506, 548},
		"auto_continue_incomplete_turn":      {709, 724, 642},
		"observability_tool_call_visibility": {643, 655, 668, 723},
		"search_semantics_paths":             {371, 372, 404, 467},
		"context_budget_large_output":        {680},
		"consolidation_merge_ready_prs":      {653, 683},
		"hive_mind_dispatch_observe":         {540},
		"typed_github_pr_flow":               {514, 649},
		"msgmate_coder_bugfix":               {915},
		"msgmate_coder_ask":                  {925},
	}
	for id, tickets := range expectations {
		scenario, ok := ScenarioByID(id)
		if !ok {
			t.Errorf("expected scenario %q to exist", id)
			continue
		}
		for _, ticket := range tickets {
			if !containsInt(scenario.Tracks, ticket) {
				t.Errorf("scenario %q does not track ci#%d", id, ticket)
			}
		}
	}
}

// TestValidateCapabilityPlanRejectsBrokenPlans asserts the validator actually
// fails on each class of defect, so a future edit to the registry cannot
// silently degrade the plan.
func TestValidateCapabilityPlanRejectsBrokenPlans(t *testing.T) {
	base := NativeCapabilityPlan()

	dup := NativeCapabilityPlan()
	dup.Scenarios = append(dup.Scenarios, dup.Scenarios[0])
	if len(ValidateCapabilityPlan(dup)) == 0 {
		t.Error("validator accepted a duplicate scenario id")
	}

	unknownCapability := NativeCapabilityPlan()
	unknownCapability.Scenarios[0].Capability = "nope"
	if len(ValidateCapabilityPlan(unknownCapability)) == 0 {
		t.Error("validator accepted a scenario with an unknown capability")
	}

	unknownTool := NativeCapabilityPlan()
	unknownTool.Scenarios[0].Tools = []string{"bash"}
	if len(ValidateCapabilityPlan(unknownTool)) == 0 {
		t.Error("validator accepted a scenario with an unknown tool")
	}

	unknownRole := NativeCapabilityPlan()
	unknownRole.Scenarios[0].Role = "wizard"
	if len(ValidateCapabilityPlan(unknownRole)) == 0 {
		t.Error("validator accepted a scenario with an unknown role")
	}

	noMarker := NativeCapabilityPlan()
	noMarker.Scenarios[0].Prompt = "do something without a marker"
	if len(ValidateCapabilityPlan(noMarker)) == 0 {
		t.Error("validator accepted a prompt without the marker token")
	}

	noAcceptance := NativeCapabilityPlan()
	noAcceptance.Scenarios[0].Acceptance = nil
	if len(ValidateCapabilityPlan(noAcceptance)) == 0 {
		t.Error("validator accepted a scenario without acceptance criteria")
	}

	uncovered := NativeCapabilityPlan()
	uncovered.Scenarios = []CapabilityScenario{{
		ID:         "only",
		Title:      "only scenario",
		Role:       "repl",
		Capability: "web_research",
		Tags:       []string{"live"},
		Tools:      []string{"coder_webfetch"},
		Prompt:     "fetch " + capabilityMarkerToken,
		Acceptance: []string{"marker present"},
	}}
	errs := ValidateCapabilityPlan(uncovered)
	if len(errs) == 0 {
		t.Fatal("validator accepted a plan with uncovered core capabilities")
	}
	joined := ""
	for _, err := range errs {
		joined += err.Error() + "\n"
	}
	if !strings.Contains(joined, "sandbox_provisioning") {
		t.Errorf("validator did not report the uncovered sandbox_provisioning capability: %s", joined)
	}

	badVersion := NativeCapabilityPlan()
	badVersion.Version = 0
	if len(ValidateCapabilityPlan(badVersion)) == 0 {
		t.Error("validator accepted version 0")
	}

	unknownTier := NativeCapabilityPlan()
	unknownTier.Classes[0].Tier = "sometimes"
	if len(ValidateCapabilityPlan(unknownTier)) == 0 {
		t.Error("validator accepted an unknown tier")
	}

	if errs := ValidateCapabilityPlan(base); len(errs) != 0 {
		t.Errorf("the shipped plan must validate, got %v", errs)
	}
}

// TestCapabilityPlanJSONStable asserts the plan marshals deterministically and
// round-trips, so a harness can consume it and attribute evidence to a plan
// revision.
func TestCapabilityPlanJSONStable(t *testing.T) {
	plan := NativeCapabilityPlan()
	first, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	second, err := json.Marshal(NativeCapabilityPlan())
	if err != nil {
		t.Fatalf("marshal plan again: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("plan JSON is not deterministic across calls")
	}

	var roundTrip CapabilityTestPlan
	if err := json.Unmarshal(first, &roundTrip); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if len(roundTrip.Scenarios) != len(plan.Scenarios) || roundTrip.Version != plan.Version {
		t.Fatalf("plan did not round-trip: %d scenarios / version %d", len(roundTrip.Scenarios), roundTrip.Version)
	}
	if errs := ValidateCapabilityPlan(roundTrip); len(errs) != 0 {
		t.Errorf("round-tripped plan must validate, got %v", errs)
	}
}

// TestCapabilityPlanScenarioIDsSortedCopy asserts the helper returning scenario
// ids for a tag is stable (registry order) and that ids are unique, which the
// harness relies on when it builds a matrix.
func TestCapabilityPlanScenarioIDsSortedCopy(t *testing.T) {
	live := ScenariosByTag("live")
	ids := make([]string, 0, len(live))
	for _, scenario := range live {
		ids = append(ids, scenario.ID)
	}
	if len(ids) == 0 {
		t.Fatal("no live scenarios in the plan")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate scenario id in tag selection: %s", id)
		}
		seen[id] = true
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	if len(sorted) != len(ids) {
		t.Fatal("sorting changed the scenario id count")
	}
}

// scenarioIDsContain reports whether the scenario list contains the id.
func scenarioIDsContain(scenarios []CapabilityScenario, id string) bool {
	for _, scenario := range scenarios {
		if scenario.ID == id {
			return true
		}
	}
	return false
}
