package nativecoder

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ci#582 (umbrella ci#585): the flip and rollback config deltas of the opencode
// retirement, derived from the flip registry in rollout.go.
//
// A delta entry is configuration, never a secret: it names the configured bot,
// the runtime it runs on today and the runtime it must run on after the change.
// Both directions are rendered from the single flip description
// (NativeBotStages), so a hand-maintained delta cannot drift from the code the
// fleet actually runs and the rollback of a flip is mechanically its inverse.
//
// The deltas are applied through `publish-open-chat-config.yaml` (vault-sync),
// which takes a DRY_RUN pass first: the apply plan is part of the data, so the
// private runbook, the ticket comment and the report job all show the same
// order of operations.
//
// No secrets, tokens or deployment configuration are part of these deltas: bot
// names, runtime names and the vault-sync *path* only.

// OpencodeRetirementDeltaAction values.
const (
	// OpencodeRetirementDeltaFlip moves one configured bot onto its family's
	// native runtime.
	OpencodeRetirementDeltaFlip = "flip"
	// OpencodeRetirementDeltaRollback moves it back onto the legacy runtime for
	// the rollback window.
	OpencodeRetirementDeltaRollback = "rollback"
)

// OpencodeRetirementDeltaEntry is one config mutation of the migration: the
// configured bot, the runtime it leaves and the runtime it moves to.
type OpencodeRetirementDeltaEntry struct {
	// Stage is the family key the bot belongs to.
	Stage string `json:"stage"`
	// Bot is the configured agent/bot name the delta touches.
	Bot string `json:"bot"`
	// From is the runtime the bot runs on today.
	From string `json:"from"`
	// To is the runtime it must run on after the delta is applied.
	To string `json:"to"`
	// Action is OpencodeRetirementDeltaFlip or ...Rollback.
	Action string `json:"action"`
}

// OpencodeRetirementFlipDelta returns the delta that moves every opencode-era
// bot onto its family's native runtime, family by family in flip order (the
// coordinating runtime last, as in NativeBotStages).
func OpencodeRetirementFlipDelta() []OpencodeRetirementDeltaEntry {
	var out []OpencodeRetirementDeltaEntry
	for _, stage := range NativeBotStages() {
		for _, legacy := range stage.LegacyBots {
			out = append(out, OpencodeRetirementDeltaEntry{
				Stage:  stage.Stage,
				Bot:    legacy,
				From:   OpenCodeLegacyRuntime,
				To:     stage.NativeRuntime,
				Action: OpencodeRetirementDeltaFlip,
			})
		}
	}
	return out
}

// OpencodeRetirementRollbackDelta is the inverse delta: it restores the legacy
// runtime of every bot, in reverse flip order, so a rollback unwinds the
// migration from the coordinating family inwards and the last family rolled
// back is the first one that flipped.
func OpencodeRetirementRollbackDelta() []OpencodeRetirementDeltaEntry {
	flip := OpencodeRetirementFlipDelta()
	out := make([]OpencodeRetirementDeltaEntry, 0, len(flip))
	for i := len(flip) - 1; i >= 0; i-- {
		entry := flip[i]
		entry.From, entry.To = entry.To, entry.From
		entry.Action = OpencodeRetirementDeltaRollback
		out = append(out, entry)
	}
	return out
}

// PendingOpencodeRetirementFlipDelta returns the flip entries still outstanding
// for the observed fleet, so a report can show the delta against the live
// configuration instead of against a fleet where nothing has flipped yet. A
// family counts as flipped exactly like the retirement gate decides it
// (familyIsNative), so the delta and the gate cannot disagree.
func PendingOpencodeRetirementFlipDelta(state OpencodeRetirementState) []OpencodeRetirementDeltaEntry {
	var out []OpencodeRetirementDeltaEntry
	for _, entry := range OpencodeRetirementFlipDelta() {
		if stage, ok := NativeBotStageForLegacyBot(entry.Bot); ok && familyIsNative(state.ConfiguredRuntimes, stage) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// ValidateOpencodeRetirementDeltas checks that a flip delta and its rollback
// delta are exact inverses and that every opencode-era bot of the registry is
// covered exactly once per direction, so a partial rollback cannot be rendered.
func ValidateOpencodeRetirementDeltas(flip, rollback []OpencodeRetirementDeltaEntry) error {
	if len(flip) == 0 {
		return fmt.Errorf("flip delta is empty")
	}
	if len(rollback) != len(flip) {
		return fmt.Errorf("rollback delta covers %d of %d flipped bots", len(rollback), len(flip))
	}
	seen := map[string]bool{}
	for _, entry := range flip {
		switch {
		case strings.TrimSpace(entry.Bot) == "":
			return fmt.Errorf("flip delta has an entry without a bot")
		case seen[entry.Bot]:
			return fmt.Errorf("bot %q appears twice in the flip delta", entry.Bot)
		case entry.Action != OpencodeRetirementDeltaFlip:
			return fmt.Errorf("flip delta entry %q has action %q", entry.Bot, entry.Action)
		case entry.To == entry.From:
			return fmt.Errorf("flip delta entry %q does not change the runtime (%q)", entry.Bot, entry.To)
		case !IsNativeBotRuntime(entry.To):
			return fmt.Errorf("flip delta entry %q targets %q, which is not a native runtime", entry.Bot, entry.To)
		}
		if _, known := NativeBotStageForLegacyBot(entry.Bot); !known {
			return fmt.Errorf("flip delta entry %q is not an opencode-era bot of the flip registry", entry.Bot)
		}
		seen[entry.Bot] = true
	}

	back := map[string]OpencodeRetirementDeltaEntry{}
	for _, entry := range rollback {
		if entry.Action != OpencodeRetirementDeltaRollback {
			return fmt.Errorf("rollback delta entry %q has action %q", entry.Bot, entry.Action)
		}
		back[entry.Bot] = entry
	}
	for _, entry := range flip {
		inverse, ok := back[entry.Bot]
		if !ok {
			return fmt.Errorf("rollback delta misses the flipped bot %q", entry.Bot)
		}
		if inverse.From != entry.To || inverse.To != entry.From {
			return fmt.Errorf("rollback of %q is not the inverse of its flip (%s -> %s)", entry.Bot, inverse.From, inverse.To)
		}
	}
	return nil
}

// ValidateOpencodeRetirementDeltaPlan validates the shipped deltas.
func ValidateOpencodeRetirementDeltaPlan() error {
	return ValidateOpencodeRetirementDeltas(OpencodeRetirementFlipDelta(), OpencodeRetirementRollbackDelta())
}

// OpencodeRetirementDeltaJSON renders a delta as stable JSON (one entry per
// config mutation, in apply order) for the publish run and for run evidence.
func OpencodeRetirementDeltaJSON(delta []OpencodeRetirementDeltaEntry) (string, error) {
	if delta == nil {
		delta = []OpencodeRetirementDeltaEntry{}
	}
	encoded, err := json.Marshal(struct {
		Entries []OpencodeRetirementDeltaEntry `json:"entries"`
	}{Entries: delta})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// OpencodeRetirementDeltaApplyPlan is the ordered operator procedure for
// applying a delta: render it, apply it through publish-open-chat-config.yaml
// (vault-sync) with DRY_RUN=true first, then apply for real and keep the
// opposite direction at hand for the whole rollback window. An unknown action
// is treated as the flip direction.
func OpencodeRetirementDeltaApplyPlan(action string) []string {
	switch action {
	case OpencodeRetirementDeltaRollback:
		return []string{
			fmt.Sprintf("render the %s delta (%d entries) with OpencodeRetirementDeltaJSON", OpencodeRetirementDeltaRollback, len(OpencodeRetirementRollbackDelta())),
			"apply it through publish-open-chat-config.yaml (vault-sync) with DRY_RUN=true and inspect the planned mutations",
			"re-run the same publish with DRY_RUN=false and confirm the " + OpenCodeLegacyRuntime + " bots are in rotation again",
			"watch " + strings.Join(nativeBotMonitors(), " + ") + " and record the rollback in the migration state (RollbackProvenOn) before the fleet moves again",
		}
	default:
		return []string{
			fmt.Sprintf("render the %s delta (%d entries) with OpencodeRetirementDeltaJSON", OpencodeRetirementDeltaFlip, len(OpencodeRetirementFlipDelta())),
			"apply it through publish-open-chat-config.yaml (vault-sync) with DRY_RUN=true and inspect the planned mutations",
			"re-run the same publish with DRY_RUN=false, then watch " + strings.Join(nativeBotMonitors(), " + "),
			fmt.Sprintf("keep the %s delta (%d entries) at hand for the %d day rollback window", OpencodeRetirementDeltaRollback, len(OpencodeRetirementRollbackDelta()), NativeBotRollbackWindowDays),
		}
	}
}

// RenderOpencodeRetirementDeltaMarkdown renders a delta and its apply plan as the
// table a ticket comment or the private runbook shows.
func RenderOpencodeRetirementDeltaMarkdown(delta []OpencodeRetirementDeltaEntry) string {
	var b strings.Builder
	b.WriteString("| family | bot | from | to | action |\n")
	b.WriteString("|--------|-----|------|----|--------|\n")
	for _, entry := range delta {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			entry.Stage, entry.Bot, entry.From, entry.To, entry.Action)
	}
	b.WriteString("\nApplied through `publish-open-chat-config.yaml` (vault-sync), `DRY_RUN=true` first:\n\n")
	for i, step := range OpencodeRetirementDeltaApplyPlan(OpencodeRetirementDeltaFlip) {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step)
	}
	return b.String()
}
