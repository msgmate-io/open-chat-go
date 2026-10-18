package nativecoder

import (
	"strings"
)

// Fleet audit thresholds (ci#733, "Development lead: fleet-moderation toolkit").
//
// A managed interaction is considered to be thrashing when any of the
// thresholds below is hit; the development lead then stops the interaction and
// resumes it once with a narrowed prompt instead of letting it burn the shared
// provider tool budget.
const (
	// FleetAuditCallThreshold is the number of tool calls after which an
	// interaction that produced no edit at all counts as thrashing.
	FleetAuditCallThreshold = 30
	// FleetAuditDuplicateThreshold is the number of *exact* duplicate tool
	// calls (same tool, same arguments) after which an interaction counts as
	// thrashing.
	FleetAuditDuplicateThreshold = 3
	// FleetAuditTerminalStreakThreshold is the number of consecutive
	// non-productive terminal reasons (e.g. no_usable_tool_call or
	// turn_timeout) after which an interaction counts as thrashing.
	FleetAuditTerminalStreakThreshold = 2
	// FleetAuditStallAgeSeconds mirrors the interaction stall threshold used
	// by the agent interaction view.
	FleetAuditStallAgeSeconds = 10 * 60

	// FleetAuditNarrowCallBudget is the tool-call budget the development lead
	// grants when it rescues a thrashing interaction.
	FleetAuditNarrowCallBudget = 15
)

// Fleet audit recommendation kinds. They are plain strings so they can be
// surfaced verbatim on the agent interaction view / tools.
const (
	FleetAuditActionOK            = "ok"
	FleetAuditActionStopAndNarrow = "stop_and_narrow"
	FleetAuditActionContinueOnce  = "continue_once"
	FleetAuditActionReadyToMerge  = "ready_to_merge"
)

// fleetAuditTerminalReasons lists the terminal reasons that mean "this turn
// produced nothing" (ci#733 Part A).
var fleetAuditTerminalReasons = map[string]bool{
	"no_usable_tool_call": true,
	"turn_timeout":        true,
	"stall_no_first_turn": true,
	"empty_reply":         true,
}

// FleetAuditInput is the raw, DB-derived signal set for one managed
// interaction. Every field is optional so callers can fill in whatever the
// interaction view already exposes.
type FleetAuditInput struct {
	// ToolCalls is the number of tool calls recorded for the interaction.
	ToolCalls int `json:"tool_calls"`
	// Edits is the number of write/edit tool calls recorded.
	Edits int `json:"edits"`
	// ExactDuplicateCalls counts tool calls repeated with identical arguments.
	ExactDuplicateCalls int `json:"exact_duplicate_calls"`
	// TerminalReasons are the per-turn terminal reasons, oldest first.
	TerminalReasons []string `json:"terminal_reasons,omitempty"`
	// StallReason is the current stall reason ("" when the interaction is not
	// stalled).
	StallReason string `json:"stall_reason,omitempty"`
	// StallAgeSeconds is how long the interaction has been without a turn.
	StallAgeSeconds int `json:"stall_age_seconds,omitempty"`
	// NoSummary is set when the interaction stopped without a final summary.
	NoSummary bool `json:"no_summary,omitempty"`
	// FinishedWithoutPR is set when the interaction finished but never opened
	// a pull request.
	FinishedWithoutPR bool `json:"finished_without_pr,omitempty"`
	// ReadyPR is set when the interaction's pull request is green and waiting
	// for the development lead to drive the consolidation.
	ReadyPR bool `json:"ready_pr,omitempty"`
}

// FleetAudit is the deterministic verdict the development lead acts on.
type FleetAudit struct {
	Thrashing         bool   `json:"thrashing"`
	Stalled           bool   `json:"stalled"`
	Action            string `json:"action"`
	Reasons           []string
	RecommendedPrompt string `json:"recommended_prompt,omitempty"`
}

// AuditFleetInteraction classifies one managed interaction and returns the
// recommended action plus the prompt the lead should send when it acts.
//
// It is intentionally pure: the DB/agent-API layer collects FleetAuditInput and
// this function decides, so the moderation policy is unit-testable and shared
// between the audit tool, the rescue tool and the consolidation driver.
func AuditFleetInteraction(in FleetAuditInput) FleetAudit {
	out := FleetAudit{Reasons: []string{}}

	if in.ToolCalls >= FleetAuditCallThreshold && in.Edits == 0 {
		out.Reasons = append(out.Reasons, "tool_calls_without_edits")
	}
	if in.ExactDuplicateCalls >= FleetAuditDuplicateThreshold {
		out.Reasons = append(out.Reasons, "exact_duplicate_calls")
	}
	if trailingTerminalReasonStreak(in.TerminalReasons) >= FleetAuditTerminalStreakThreshold {
		out.Reasons = append(out.Reasons, "repeated_terminal_reason")
	}
	out.Thrashing = len(out.Reasons) > 0

	if in.StallReason != "" || in.StallAgeSeconds >= FleetAuditStallAgeSeconds {
		out.Stalled = true
		out.Reasons = append(out.Reasons, "stalled")
	}

	switch {
	case out.Thrashing:
		out.Action = FleetAuditActionStopAndNarrow
		out.RecommendedPrompt = fleetAuditNarrowPrompt(in)
	case out.Stalled || in.NoSummary:
		out.Action = FleetAuditActionContinueOnce
		out.RecommendedPrompt = fleetAuditResumePrompt(in)
	case in.FinishedWithoutPR:
		out.Action = FleetAuditActionContinueOnce
		out.RecommendedPrompt = fleetAuditOpenPRPrompt()
	case in.ReadyPR:
		out.Action = FleetAuditActionReadyToMerge
	default:
		out.Action = FleetAuditActionOK
	}
	return out
}

// trailingTerminalReasonStreak counts how many of the most recent turns ended
// with a non-productive terminal reason in a row.
func trailingTerminalReasonStreak(reasons []string) int {
	streak := 0
	for i := len(reasons) - 1; i >= 0; i-- {
		if !fleetAuditTerminalReasons[strings.TrimSpace(strings.ToLower(reasons[i]))] {
			break
		}
		streak++
	}
	return streak
}

func fleetAuditNarrowPrompt(in FleetAuditInput) string {
	var b strings.Builder
	b.WriteString("Stop and narrow: this turn produced no progress")
	if in.Edits == 0 && in.ToolCalls >= FleetAuditCallThreshold {
		b.WriteString(" (too many tool calls without a single edit)")
	}
	b.WriteString(". Resume with a single, explicit objective and a budget of ")
	b.WriteString("[")
	b.WriteString(itoa(FleetAuditNarrowCallBudget))
	b.WriteString("] tool calls: (1) read only the files named in the objective, ")
	b.WriteString("(2) make the smallest edit that satisfies it, (3) run the focused test, ")
	b.WriteString("(4) report the diff. Do not re-read files you already read and do not repeat a call you already made.")
	return b.String()
}

func fleetAuditResumePrompt(in FleetAuditInput) string {
	if in.StallReason != "" {
		return "Resume once: the interaction stalled (" + in.StallReason + "). Post a one-paragraph status (done / in progress / blocked), then continue with the next concrete step."
	}
	return "Resume once and finish the turn with a short summary: what changed, what is verified and what is still pending."
}

func fleetAuditOpenPRPrompt() string {
	return "The work is finished but no pull request exists. Open the PR now (feature PRs carry only the `test` and `build` labels), then report the PR URL in one line."
}

// itoa avoids pulling strconv into the prompt helpers above.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
