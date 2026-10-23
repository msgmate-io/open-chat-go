package nativecoder

import (
	"strings"
	"testing"
)

func TestOpencodeRetirementEvidencePerStep(t *testing.T) {
	blocked := OpencodeRetirementEvidence(OpencodeRetirementState{})
	byID := map[string]RetirementEvidence{}
	for _, row := range blocked {
		if _, dup := byID[row.ID]; dup {
			t.Fatalf("duplicate evidence row %q", row.ID)
		}
		byID[row.ID] = row
	}
	if len(byID) != len(OpencodeRetirementPlan()) {
		t.Fatalf("expected one row per checklist step, got %d", len(byID))
	}
	for _, step := range OpencodeRetirementPlan() {
		row, ok := byID[step.ID]
		if !ok {
			t.Fatalf("checklist step %q has no evidence row", step.ID)
		}
		if row.Status != RetirementEvidenceBlocked {
			t.Fatalf("step %q must be blocked from an empty state, got %q", step.ID, row.Status)
		}
		if len(row.Detail) == 0 {
			t.Fatalf("blocked step %q must name at least one reason", step.ID)
		}
	}

	readyState := OpencodeRetirementState{
		ConfiguredRuntimes: fullyNativeRuntimes(),
		RolledOutThrough:   stageLead,
		RollbackProvenOn:   stageRepl,
		NativeStableDays:   2 * NativeBotStabilityWindowDays,
		ParitySignedOff:    true,
		RetirementDeltaOK:  true,
		ABValidationDone:   true,
	}
	ready := OpencodeRetirementEvidence(readyState)
	for _, row := range ready {
		if row.Status != RetirementEvidenceDone {
			t.Fatalf("ready state must mark %q done, got %q (%v)", row.ID, row.Status, row.Detail)
		}
	}
	if md := RenderOpencodeRetirementEvidenceMarkdown(readyState); !strings.Contains(md, "| done |") {
		t.Fatalf("markdown must render the done status:\n%s", md)
	}
}
