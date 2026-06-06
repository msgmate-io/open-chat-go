package nativecoder

import (
	"fmt"
	"strings"
)

// ci#585 umbrella: the retirement gate of opencoderetirement.go answers "may the
// opencode integration stop?" as a single boolean plus blockers. The umbrella
// checklist of the ticket has five steps with a ticket each, so this file breaks
// the same gate down per step: every checklist entry gets the status the gate
// implies and the blocker lines that belong to it. The evidence is derived from
// OpencodeRetirementBlockers, never hand-written, so a step cannot be reported as
// signed off while its blocker is still listed.

// RetirementEvidenceStatus values.
const (
	// RetirementEvidenceDone marks a checklist step with no blocker left.
	RetirementEvidenceDone = "done"
	// RetirementEvidenceBlocked marks a checklist step whose gate is still
	// blocked; Detail names the reasons.
	RetirementEvidenceBlocked = "blocked"
)

// RetirementEvidence is one checklist step plus the gate result behind it.
type RetirementEvidence struct {
	ID     string
	Ticket string
	Title  string
	Status string
	Detail []string
}

// retirementEvidenceFragments maps a checklist step onto the blocker fragments
// that belong to it. Fragments are matched case-insensitively against the gate
// lines, so the mapping stays stable when a blocker message is reworded.
func retirementEvidenceFragments(id string) []string {
	switch id {
	case "parity_signoff":
		return []string{"ci#581", "parity gap"}
	case "config_delta":
		return []string{"ci#582", "flip/rollback delta"}
	case "ab_validation":
		return []string{"ci#583", "a/b validation"}
	case "rollout":
		return []string{"rolled out", "rollback was never proven", "fleet has been native", "still run"}
	default:
		return nil
	}
}

// OpencodeRetirementEvidence returns one row per umbrella step, in checklist
// order. The retirement step itself is only done when the whole gate is open.
func OpencodeRetirementEvidence(state OpencodeRetirementState) []RetirementEvidence {
	blockers := OpencodeRetirementBlockers(state)
	ready := len(blockers) == 0
	out := make([]RetirementEvidence, 0, len(OpencodeRetirementPlan()))
	for _, step := range OpencodeRetirementPlan() {
		detail := []string{}
		if step.ID == "retire_opencode" {
			if !ready {
				detail = append(detail, blockers...)
			}
		} else {
			fragments := retirementEvidenceFragments(step.ID)
			for _, blocker := range blockers {
				line := strings.ToLower(blocker)
				for _, fragment := range fragments {
					if strings.Contains(line, strings.ToLower(fragment)) {
						detail = append(detail, blocker)
						break
					}
				}
			}
		}
		status := RetirementEvidenceDone
		if len(detail) > 0 {
			status = RetirementEvidenceBlocked
		}
		out = append(out, RetirementEvidence{
			ID: step.ID, Ticket: step.Ticket, Title: step.Title,
			Status: status, Detail: detail,
		})
	}
	return out
}

// RenderOpencodeRetirementEvidenceMarkdown renders the per-step evidence for a
// ticket comment; it is the checklist form of the ci#585 gate.
func RenderOpencodeRetirementEvidenceMarkdown(state OpencodeRetirementState) string {
	var b strings.Builder
	b.WriteString("| step | ticket | status | reasons |\n")
	b.WriteString("|------|--------|--------|---------|\n")
	for _, row := range OpencodeRetirementEvidence(state) {
		reasons := "-"
		if len(row.Detail) > 0 {
			reasons = strings.Join(row.Detail, "; ")
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", row.Title, row.Ticket, row.Status, reasons))
	}
	return b.String()
}
