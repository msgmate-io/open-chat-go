package nativecoder

import (
	"fmt"
	"strings"
)

// Phase exit-criteria evidence (ci#514).
//
// The plan in bashretirement.go declares, per phase, an ordered list of exit
// criteria as prose. retirementgate.go already answers the *category* half of a
// phase (is the replacing typed surface shipped, has the category's share fallen
// below the threshold), but the remaining criteria - "the prompt prefers the
// typed tools", "a write is re-read and verified", "the capability scenario
// completes without coder_bash" - were only prose: a round closing a phase had
// to re-read the ticket and argue by hand which criteria it had actually met,
// and nothing stopped a phase from being declared done while one of them had
// never been checked at all.
//
// This file makes that checklist explicit and machine-checked. Every criterion
// of every phase gets a stable id (`<phase>/criterion_<n>`), a status and the
// evidence it was derived from:
//
//   - satisfied: a check passed and names the evidence it used,
//   - blocked: a check ran and failed, with the reason,
//   - unattested: no machine-checked evidence is wired for that criterion yet.
//
// "unattested" is deliberately a first-class status and deliberately blocks the
// phase: the honest answer to "is criterion 4 met?" is "nobody checked", not a
// silent pass. That is what keeps the phase-1 exit criteria from degrading into
// a claim. As evidence sources get wired (write-verification reports, capability
// scenario runs, prompt assertions) the criteria move from unattested to
// satisfied/blocked without any change to the plan or the metric.
//
// The evaluator reuses EvaluateBashRetirementPhases for the category criteria,
// so the criterion list and the coverage metric can never drift apart: the same
// report drives both.

// The plan phase this file has machine-checked criteria for. Other phases are
// reported as unattested so the checklist is complete for the whole plan.
const bashRetirementGitHubPhaseID = "phase_1_github"

// Criterion statuses.
const (
	// BashCriterionSatisfied: the criterion was checked and holds.
	BashCriterionSatisfied = "satisfied"
	// BashCriterionBlocked: the criterion was checked and does not hold.
	BashCriterionBlocked = "blocked"
	// BashCriterionUnattested: no machine-checked evidence is wired yet.
	BashCriterionUnattested = "unattested"
)

// BashRetirementCriterionEvidence is the machine-checked state of one phase exit
// criterion.
type BashRetirementCriterionEvidence struct {
	// ID is the stable criterion id: "<phase>/criterion_<n>".
	ID string `json:"id"`
	// PhaseID is the plan's stable phase id.
	PhaseID string `json:"phase_id"`
	// Order is the phase sequence.
	Order int `json:"order"`
	// Index is the criterion's 1-based position in the phase's list.
	Index int `json:"index"`
	// Text is the criterion exactly as the plan declares it.
	Text string `json:"text"`
	// Status is one of the BashCriterion* constants.
	Status string `json:"status"`
	// Evidence names what the status was derived from, so a satisfied
	// criterion is auditable rather than asserted.
	Evidence []string `json:"evidence,omitempty"`
	// Blockers are the reasons a blocked/unattested criterion is not met.
	Blockers []string `json:"blockers,omitempty"`
}

// BashRetirementPhaseCriteria is the criterion checklist of one plan phase.
type BashRetirementPhaseCriteria struct {
	// ID is the plan's stable phase id.
	ID string `json:"id"`
	// Order is the phase sequence.
	Order int `json:"order"`
	// Title is the phase's one-line description.
	Title string `json:"title"`
	// Measured reports whether the evaluation saw a non-empty sample, so an
	// all-green phase cannot be claimed from zero evidence.
	Measured bool `json:"measured"`
	// Criteria is one entry per declared exit criterion, in plan order.
	Criteria []BashRetirementCriterionEvidence `json:"criteria,omitempty"`
	// Ready reports whether every criterion is satisfied on a measured sample.
	Ready bool `json:"ready"`
	// Attested reports whether every criterion has machine-checked evidence
	// (ie none is unattested). Ready implies Attested, never the reverse.
	Attested bool `json:"attested"`
	// Blockers aggregates the unmet criteria as "<criterion id>: <reason>".
	Blockers []string `json:"blockers,omitempty"`
}

// BashRetirementEvidence is the evidence bundle the criteria evaluator checks a
// phase against. Every field may be empty/nil, in which case the criteria it
// would attest stay unattested instead of silently passing.
type BashRetirementEvidence struct {
	// Coverage is the shell-call coverage report, the same evidence the
	// category gate uses.
	Coverage BashCoverageReport
	// GitHub is the phase-1 GitHub shell translation report.
	GitHub *GitHubShellTranslationReport
	// CoderSystemPrompt is the rendered coder system prompt a run really uses,
	// so the typed-first criterion is measured rather than assumed.
	CoderSystemPrompt string
}

// EvaluateBashRetirementCriteria turns the plan's prose exit criteria into
// machine-checked evidence, using the same coverage report as the category
// gate. `github` is the phase-1 GitHub shell translation report; it may be nil,
// in which case the criteria it would attest stay unattested instead of
// silently passing.
func EvaluateBashRetirementCriteria(report BashCoverageReport, github *GitHubShellTranslationReport) []BashRetirementPhaseCriteria {
	return EvaluateBashRetirementCriteriaWithEvidence(BashRetirementEvidence{
		Coverage: report,
		GitHub:   github,
	})
}

// EvaluateBashRetirementCriteriaWithEvidence is the full evaluator: it checks
// every phase against the whole evidence bundle (coverage, GitHub translation
// and the rendered coder system prompt).
func EvaluateBashRetirementCriteriaWithEvidence(evidence BashRetirementEvidence) []BashRetirementPhaseCriteria {
	report := evidence.Coverage
	github := evidence.GitHub
	plan := BashRetirementPlanSnapshot()
	gates := make(map[string]BashRetirementPhaseGate)
	for _, gate := range EvaluateBashRetirementPhases(report) {
		gates[gate.ID] = gate
	}
	out := make([]BashRetirementPhaseCriteria, 0, len(plan.Phases))
	for _, phase := range plan.Phases {
		entry := BashRetirementPhaseCriteria{
			ID:       phase.ID,
			Order:    phase.Order,
			Title:    phase.Title,
			Measured: report.ClassifiedCalls > 0,
		}
		gate := gates[phase.ID]
		for i, text := range phase.ExitCriteria {
			criterion := BashRetirementCriterionEvidence{
				ID:      fmt.Sprintf("%s/criterion_%d", phase.ID, i+1),
				PhaseID: phase.ID,
				Order:   phase.Order,
				Index:   i + 1,
				Text:    text,
			}
			if phase.ID == bashRetirementGitHubPhaseID {
				criterion = evaluateGitHubPhaseCriterion(criterion, phase, gate, report, github, evidence.CoderSystemPrompt)
			} else {
				criterion.Status = BashCriterionUnattested
				criterion.Blockers = append(criterion.Blockers,
					"no machine-checked evidence is wired for this phase yet")
			}
			entry.Criteria = append(entry.Criteria, criterion)
		}
		entry.Ready = entry.Measured && len(entry.Criteria) > 0
		entry.Attested = len(entry.Criteria) > 0
		for _, criterion := range entry.Criteria {
			if criterion.Status == BashCriterionUnattested {
				entry.Attested = false
			}
			if criterion.Status != BashCriterionSatisfied {
				entry.Ready = false
			}
			reasons := criterion.Blockers
			if len(reasons) == 0 && criterion.Status != BashCriterionSatisfied {
				reasons = []string{criterion.Status}
			}
			for _, reason := range reasons {
				entry.Blockers = append(entry.Blockers, criterion.ID+": "+reason)
			}
		}
		out = append(out, entry)
	}
	return out
}

// evaluateGitHubPhaseCriterion checks one phase-1 criterion. The criterion
// order is the plan's, so a reordered plan fails loudly in the tests instead of
// silently checking the wrong thing.
func evaluateGitHubPhaseCriterion(criterion BashRetirementCriterionEvidence, phase BashRetirementPhase, gate BashRetirementPhaseGate, report BashCoverageReport, github *GitHubShellTranslationReport, coderSystemPrompt string) BashRetirementCriterionEvidence {
	switch criterion.Index {
	case 1:
		// "the typed GitHub tools are shipped and declared by the coding
		// integration": the plan registry is authoritative about the shipped
		// surface and the typed tools it declares.
		if len(phase.Categories) == 0 {
			criterion.Status = BashCriterionBlocked
			criterion.Blockers = append(criterion.Blockers, "the phase declares no category to retire")
			return criterion
		}
		for _, id := range phase.Categories {
			category, known := BashRetirementCategoryByID(id)
			if !known {
				criterion.Status = BashCriterionBlocked
				criterion.Blockers = append(criterion.Blockers, id+": category is not in the plan's registry")
				continue
			}
			if category.Guarded {
				continue
			}
			if !category.Shipped {
				criterion.Status = BashCriterionBlocked
				criterion.Blockers = append(criterion.Blockers, id+": the replacing typed surface is not shipped")
				continue
			}
			if len(category.TargetTools) == 0 {
				criterion.Status = BashCriterionBlocked
				criterion.Blockers = append(criterion.Blockers, id+": the shipped surface declares no typed tools")
				continue
			}
			criterion.Evidence = append(criterion.Evidence, fmt.Sprintf("%s: shipped typed surface declares %d tools (%s)", id, len(category.TargetTools), strings.Join(category.TargetTools, ", ")))
		}
		if criterion.Status != BashCriterionBlocked {
			criterion.Status = BashCriterionSatisfied
		}
		return criterion
	case 2:
		// "the coder system prompt prefers the typed tools for every GitHub
		// workflow": attested from the prompt the run really uses, so the
		// criterion cannot be met by the mere existence of a constant.
		if strings.TrimSpace(coderSystemPrompt) == "" {
			criterion.Status = BashCriterionUnattested
			criterion.Blockers = append(criterion.Blockers, "no rendered coder system prompt was supplied")
			return criterion
		}
		prompt := MeasureGitHubTypedFirstPrompt(coderSystemPrompt)
		if prompt.Ready {
			criterion.Status = BashCriterionSatisfied
			criterion.Evidence = append(criterion.Evidence, fmt.Sprintf("coder system prompt carries the typed-first block and names all %d typed GitHub tools", len(prompt.Tools)))
			return criterion
		}
		criterion.Status = BashCriterionBlocked
		if !prompt.GuidanceApplied {
			criterion.Blockers = append(criterion.Blockers, "the coder system prompt does not carry the typed-first GitHub block")
		}
		if len(prompt.Missing) > 0 {
			criterion.Blockers = append(criterion.Blockers, "the coder system prompt does not name: "+strings.Join(prompt.Missing, ", "))
		}
		return criterion
	case 3:
		// "the github_cli share of shell calls is below the retirement
		// threshold": exactly what the category gate computes from the same
		// report.
		if len(gate.Categories) == 0 {
			criterion.Status = BashCriterionBlocked
			criterion.Blockers = append(criterion.Blockers, "the phase gate retires no category in this sample")
			return criterion
		}
		for _, entry := range gate.Categories {
			if entry.Retired {
				criterion.Evidence = append(criterion.Evidence, fmt.Sprintf("%s: share %.2f%% is below the %.2f%% threshold", entry.Category, entry.Share*100, entry.Threshold*100))
				continue
			}
			criterion.Status = BashCriterionBlocked
			for _, blocker := range entry.Blockers {
				criterion.Blockers = append(criterion.Blockers, entry.Category+": "+blocker)
			}
		}
		if criterion.Status != BashCriterionBlocked {
			criterion.Status = BashCriterionSatisfied
		}
		return criterion
	case 5:
		// "the capability plan's typed-GitHub scenario completes without a
		// coder_bash call": attested by the GitHub shell translation report,
		// which is the phase-1 measure of how much GitHub work the typed
		// surface can express.
		if github == nil {
			criterion.Status = BashCriterionUnattested
			criterion.Blockers = append(criterion.Blockers, "no GitHub shell translation report was supplied")
			return criterion
		}
		if github.PhaseReady && github.ResidualCommands == 0 && github.GitHubCommands > 0 {
			criterion.Status = BashCriterionSatisfied
			criterion.Evidence = append(criterion.Evidence, fmt.Sprintf("github_cli: %d/%d commands translated to typed calls, %d residual", github.TranslatedCommands, github.GitHubCommands, github.ResidualCommands))
			return criterion
		}
		criterion.Status = BashCriterionBlocked
		criterion.Blockers = append(criterion.Blockers, fmt.Sprintf("github_cli translation: %d of %d commands translated, %d residual, phase ready=%t", github.TranslatedCommands, github.GitHubCommands, github.ResidualCommands, github.PhaseReady))
		return criterion
	default:
		// Criterion 4 (write re-read and verified) needs a write-verification
		// evidence source that is not wired into this evaluator yet. It is
		// reported unattested rather than assumed.
		criterion.Status = BashCriterionUnattested
		criterion.Blockers = append(criterion.Blockers, "no machine-checked evidence is wired for this criterion yet")
		return criterion
	}
}

// BashRetirementCriteriaReadyToClose reports whether every phase's criteria are
// satisfied on a measured sample. An empty checklist is not evidence of
// readiness.
func BashRetirementCriteriaReadyToClose(criteria []BashRetirementPhaseCriteria) bool {
	if len(criteria) == 0 {
		return false
	}
	for _, phase := range criteria {
		if len(phase.Criteria) == 0 {
			continue
		}
		if !phase.Ready {
			return false
		}
	}
	return true
}
