package nativecoder

import "fmt"

// Phase exit gates (ci#514).
//
// `ComputeBashCoverage` answers "how much of the observed shell does the typed
// surface express". The plan additionally declares ordered *phases*, each with
// an explicit exit criterion: a phase closes once every category it retires has
// a shipped typed surface and its share of the observed calls has dropped below
// the plan threshold. Nothing computed that over the coverage report, so a
// round that wanted to close a phase had to read the phase status off the
// buckets by eye - and could not tell a genuinely retired category from one
// that merely looks small in a short sample.
//
// This file turns the coverage report into per-phase, machine-checked gate
// state, so the very same evidence that produces the metric also answers "may
// this phase close?" with an explicit blocker list.
//
// Two kinds of phase deliberately never report "ready", because they retire no
// category:
//
//   - the measure phase, which only defines the metric, and
//   - a phase whose only categories are guarded (the residual shell), which is
//     intended to stay on a shell forever.
//
// Guarded categories are therefore never listed, never counted as retired and
// never block a phase: demanding a share below the threshold for them would
// make every later phase unclosable.

// BashRetirementCategoryGate is the per-category part of a phase gate: whether
// the typed surface replacing the category is shipped, whether the category is
// retired, and what is still missing when it is not.
type BashRetirementCategoryGate struct {
	// Category is the plan's stable category id.
	Category string `json:"category"`
	// Share is the fraction of classified historical calls in this category.
	Share float64 `json:"share"`
	// Threshold is the plan threshold the share must fall below.
	Threshold float64 `json:"threshold"`
	// Shipped reports whether the replacing typed surface exists.
	Shipped bool `json:"shipped"`
	// Retired reports whether the category is shipped and below the threshold.
	Retired bool `json:"retired"`
	// TargetTools are the typed tools that replace the category.
	TargetTools []string `json:"target_tools,omitempty"`
	// Blockers are the reasons the category is not retired; empty when it is.
	Blockers []string `json:"blockers,omitempty"`
}

// BashRetirementPhaseGate is the gate of one plan phase.
type BashRetirementPhaseGate struct {
	// ID is the plan's stable phase id.
	ID string `json:"id"`
	// Order is the phase sequence.
	Order int `json:"order"`
	// Title is the phase's one-line description.
	Title string `json:"title"`
	// Ready reports whether every category the phase retires is retired. A
	// phase that retires no category (measure, or guarded-only) is never
	// "ready": it has nothing to close.
	Ready bool `json:"ready"`
	// Measured reports whether the gate was evaluated against a non-empty
	// sample, so an all-green phase cannot be claimed from zero evidence.
	Measured bool `json:"measured"`
	// Categories are the gates of the categories the phase retires. Guarded
	// categories are omitted: they are deliberately shell-only.
	Categories []BashRetirementCategoryGate `json:"categories,omitempty"`
	// Blockers aggregates the category blockers as "<category>: <reason>".
	Blockers []string `json:"blockers,omitempty"`
}

// EvaluateBashRetirementPhases evaluates every plan phase against a coverage
// report, using the report's own threshold so the gate can never drift from the
// metric it is derived from.
func EvaluateBashRetirementPhases(report BashCoverageReport) []BashRetirementPhaseGate {
	plan := BashRetirementPlanSnapshot()
	buckets := make(map[string]BashCoverageBucket, len(report.Buckets))
	for _, bucket := range report.Buckets {
		buckets[bucket.Category] = bucket
	}
	gates := make([]BashRetirementPhaseGate, 0, len(plan.Phases))
	for _, phase := range plan.Phases {
		gate := BashRetirementPhaseGate{
			ID:       phase.ID,
			Order:    phase.Order,
			Title:    phase.Title,
			Measured: report.ClassifiedCalls > 0,
		}
		for _, id := range phase.Categories {
			category, known := BashRetirementCategoryByID(id)
			if !known {
				gate.Blockers = append(gate.Blockers,
					fmt.Sprintf("%s: category is not in the plan's registry", id))
				continue
			}
			if category.Guarded {
				// Deliberately shell-only: never retired, never blocking.
				continue
			}
			bucket := buckets[id]
			entry := BashRetirementCategoryGate{
				Category:  id,
				Share:     bucket.Share,
				Threshold: report.Threshold,
				// The plan registry is authoritative about whether the typed
				// surface exists; the bucket merely mirrors it.
				Shipped:     category.Shipped,
				TargetTools: append([]string(nil), category.TargetTools...),
			}
			if !category.Shipped {
				entry.Blockers = append(entry.Blockers, "the replacing typed surface is not shipped")
			}
			if bucket.Share >= report.Threshold {
				entry.Blockers = append(entry.Blockers, fmt.Sprintf(
					"share %.2f%% is not below the %.2f%% threshold",
					bucket.Share*100, report.Threshold*100))
			}
			entry.Retired = len(entry.Blockers) == 0
			if !entry.Retired {
				for _, blocker := range entry.Blockers {
					gate.Blockers = append(gate.Blockers, id+": "+blocker)
				}
			}
			gate.Categories = append(gate.Categories, entry)
		}
		// Ready only when the sample is real, the phase retires at least one
		// category, and each of them is retired; measure/guarded-only phases,
		// unknown categories and empty samples always stay open.
		gate.Ready = gate.Measured && len(gate.Categories) > 0 && len(gate.Blockers) == 0
		gates = append(gates, gate)
	}
	return gates
}

// BashRetirementReadyToClose reports whether every phase that actually retires
// a category is ready, ie whether the plan's own exit criteria hold across the
// whole retirement. Phases that retire no category are skipped; an empty gate
// list is not evidence of readiness.
func BashRetirementReadyToClose(gates []BashRetirementPhaseGate) bool {
	if len(gates) == 0 {
		return false
	}
	retiring := 0
	for _, gate := range gates {
		if len(gate.Categories) == 0 {
			continue
		}
		retiring++
		if !gate.Ready {
			return false
		}
	}
	return retiring > 0
}
