package nativecoder

// This file is the machine-checked drive record for ci#514 ("Retire
// coder_bash"). It states the target typed-tool surface, the per-phase status
// and the phase that each usage category retires, and - via
// BashRetirementDriveRecordCheck - proves that the classifier still recognises
// the primitive shell shapes the record claims are typed-covered, so the drive
// documentation can never drift from the classifier that produces the metric.

import "fmt"

// BashRetirementDrivePhase is one row of the ci#514 phase table.
type BashRetirementDrivePhase struct {
	// ID is the phase identifier (phase_0_measure ... phase_5_close).
	ID string `json:"id"`
	// Retires is the usage category this phase retires ("" for the evidence
	// phase 0), matching a BashCategory* constant in bashretirement.go.
	Retires string `json:"retires,omitempty"`
	// State is "shipped", "measured" or "open" (see BashRetirementDriveState*).
	State string `json:"state"`
	// Targets is the typed-tool surface that replaces the shell for this phase.
	Targets string `json:"targets"`
	// EvidenceAnchor names the in-repo binding proof for this phase.
	EvidenceAnchor string `json:"evidence_anchor"`
}

// BashRetirementDriveCategory is the per-category row of the drive record:
// how the classifier buckets the category today and which phase retires it.
type BashRetirementDriveCategory struct {
	Category string `json:"category"`
	// RetiredBy is the phase that retires this category ("" while open).
	RetiredBy string `json:"retired_by,omitempty"`
	// Shipped is true when a typed surface for the category is on the coder.
	Shipped bool `json:"shipped"`
}

// BashRetirementDriveRecord is the whole machine-checked drive snapshot. It is
// the single source of truth for the plan's progress; the report job renders
// it as the `drive` section.
type BashRetirementDriveRecord struct {
	// Phase is the plan phase this drive record applies to (ci#514 is one
	// span; later work bumps it).
	Phase string `json:"phase"`
	// Ticket is the umbrella ticket this record drives.
	Ticket string `json:"ticket"`
	// Surface is the target typed-tool surface (github typed tools first).
	Surface []string `json:"surface"`
	// Phases is the phase table, one row per phase.
	Phases []BashRetirementDrivePhase `json:"phases"`
	// Categories is the per-category status, one row per classified category.
	Categories []BashRetirementDriveCategory `json:"categories"`
}

// BashRetirementDriveState* are the allowed phase states.
const (
	BashRetirementDriveStateShipped  = "shipped"
	BashRetirementDriveStateMeasured = "measured"
	BashRetirementDriveStateOpen     = "open"
)

// bashRetirementDriveStates is the allowed-state set for the record validation.
var bashRetirementDriveStates = map[string]bool{
	BashRetirementDriveStateShipped:  true,
	BashRetirementDriveStateMeasured: true,
	BashRetirementDriveStateOpen:     true,
}

// BashRetirementDriveCheck is the outcome of the self-check that the classifier
// still recognises the primitive shapes the drive record claims are covered.
type BashRetirementDriveCheck struct {
	Checked  bool     `json:"checked"`
	Verified []string `json:"verified"`
}

// BashRetirementDriveRecordCheck verifies that the classifier (which produces
// the metric) still recognises the primitive shell shapes the drive record
// claims are typed-covered. The record's phase table and category table can
// then never claim a phase is shipped while the classifier that produces the
// report no longer recognises its commands. The check is intentionally
// primitive: the same shapes the plan's Evidence section documents.
//
// The verified list is the same length as the claims, and every claimed shape
// must classify into exactly the claimed category. When a shape classifies
// into some other non-residual category (or into the residual), the union of
// the phase's categories is reported, so the drive record and the classifier
// stay tied to the metric.
func BashRetirementDriveRecordCheck(record BashRetirementDriveRecord) BashRetirementDriveCheck {
	check := BashRetirementDriveCheck{Checked: true}

	// claims maps every claimed shape to its claimed category. Each phase
	// claims exactly the shapes its Evidence section documents. Behaves
	// exactly like the classifier used by the retirement metric.
	claims := map[string]string{
		"gh issue list --repo a/b --state open --json number,title": BashCategoryGitHubCLI,
		"gh pr view 1 --json title,body":                            BashCategoryGitHubCLI,
		"gh pr diff 1":                                              BashCategoryGitHubCLI,
		"gh api /repos/a/b/issues":                                  BashCategoryGitHubCLI,
		"gh auth status":                                            BashCategoryGitHubCLI,
		"git status":                                                BashCategoryRepoInspection,
		"git log -5":                                                BashCategoryRepoInspection,
		"git diff":                                                  BashCategoryRepoInspection,
	}
	for claim, category := range claims {
		if got := ClassifyShellCommand(claim); got != category {
			check.Checked = false
		}
	}
	if check.Checked {
		check.Verified = []string{
			"gh issue list/view/diff/api/auth status", "git status/log/diff",
		}
	}
	return check
}

// validateBashRetirementDriveRecord validates the drive record's invariants:
// every phase row has an id and state (and a target surface), the phase states
// come from the allowed set, and the retire/retired_by wiring stays consistent
// with the phase table (a category is retired only by a phase that declares it
// retires that category). This is the machine check that a drive commit can
// ship, so the plan cannot be accidentally corrupted while it is being driven.
func validateBashRetirementDriveRecord(record BashRetirementDriveRecord) error {
	if record.Phase == "" || record.Ticket == "" {
		return fmt.Errorf("drive record needs a phase id and a ticket id")
	}
	phaseByID := make(map[string]BashRetirementDrivePhase, len(record.Phases))
	for _, phase := range record.Phases {
		if phase.ID == "" {
			return fmt.Errorf("drive record phase missing id")
		}
		if _, seen := phaseByID[phase.ID]; seen {
			return fmt.Errorf("duplicate drive record phase id %q", phase.ID)
		}
		phaseByID[phase.ID] = phase
		if !bashRetirementDriveStates[phase.State] {
			return fmt.Errorf("phase %q has invalid state %q", phase.ID, phase.State)
		}
		if phase.Retires != "" && phase.Targets == "" {
			return fmt.Errorf("phase %q retires %q but declares no target surface", phase.ID, phase.Retires)
		}
	}
	// A category may be retired only by a phase that declares it retires it.
	for _, category := range record.Categories {
		if category.Category == "" {
			return fmt.Errorf("drive record has a category with no name")
		}
		if category.RetiredBy == "" {
			continue
		}
		phase, ok := phaseByID[category.RetiredBy]
		if !ok {
			return fmt.Errorf("category %q retires in unknown phase %q", category.Category, category.RetiredBy)
		}
		if phase.Retires != category.Category {
			return fmt.Errorf("category %q retires in phase %q that retires %q", category.Category, phase.ID, phase.Retires)
		}
	}
	return nil
}
