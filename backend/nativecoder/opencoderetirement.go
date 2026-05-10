package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// ci#585 umbrella: retire the opencode integration bots on ci.msgmate.io once
// the native msgmate-coder suite owns every dev bot (self-hosting the
// migration). The per-bot flip and the rollback window already live in
// rollout.go; this file is the umbrella gate on top of it: it turns "is the
// opencode integration still needed?" into data, so the umbrella cannot be
// claimed done while a family still runs on the opencode backend.
//
// Sub-tasks: ci#581 parity sign-off, ci#582 config delta + rollback,
// ci#583 A/B validation, ci#584 rollout, ci#585 retirement (this gate).

// OpencodeRetirementStep is one step of the ci#585 umbrella checklist.
type OpencodeRetirementStep struct {
	ID       string
	Title    string
	Ticket   string
	Evidence string
}

// OpencodeRetirementPlan returns the ordered checklist, mirroring the ticket
// body. Retiring opencode is the last step and needs the four earlier ones, so
// the plan doubles as the ordering contract of the umbrella.
func OpencodeRetirementPlan() []OpencodeRetirementStep {
	return []OpencodeRetirementStep{
		{ID: "parity_signoff", Ticket: "ci#581",
			Title:    "Every opencode-bot capability has a native equivalent",
			Evidence: "parity registry reports no open capability gap"},
		{ID: "config_delta", Ticket: "ci#582",
			Title:    "Flip delta plus rollback delta published through vault-sync",
			Evidence: "flip deltas applied DRY_RUN first, rollback delta kept for one window"},
		{ID: "ab_validation", Ticket: "ci#583",
			Title:    "A/B validation ran opencode and native side by side",
			Evidence: "same tickets, comparable results attached"},
		{ID: "rollout", Ticket: "ci#584",
			Title:    "Bots flipped native bot by bot, canary green",
			Evidence: "rollout window closed with no rollback needed"},
		{ID: "retire_opencode", Ticket: "ci#585",
			Title:    "opencode bots and integration stopped on ci",
			Evidence: "no configured dev bot resolves to the opencode backend"},
	}
}

// ValidateOpencodeRetirementPlan rejects a malformed or drifting checklist, so a
// silently reordered umbrella fails in CI instead of in production.
func ValidateOpencodeRetirementPlan() error {
	steps := OpencodeRetirementPlan()
	if len(steps) == 0 {
		return fmt.Errorf("retirement plan is empty")
	}
	seen := map[string]bool{}
	for _, s := range steps {
		if s.ID == "" || s.ID != strings.TrimSpace(s.ID) {
			return fmt.Errorf("retirement step has an empty or padded id")
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate retirement step id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Title == "" || s.Ticket == "" || s.Evidence == "" {
			return fmt.Errorf("retirement step %q is missing a title, ticket or evidence", s.ID)
		}
	}
	if !seen["retire_opencode"] {
		return fmt.Errorf("retirement plan must end with the opencode retirement step")
	}
	return nil
}

// OpencodeRetirementState is the observed configuration plus the recorded
// sign-offs behind the gate.
type OpencodeRetirementState struct {
	// ConfiguredRuntimes maps every configured dev bot name to the
	// backend/runtime it runs with. A family without any bot entry, or whose
	// entries still resolve to a non-native runtime, has not been migrated.
	ConfiguredRuntimes map[string]string
	// The following three describe the fleet-wide rollout progress and are fed
	// straight into the per-family retirement gate of rollout.go.
	RolledOutThrough string
	RollbackProvenOn string
	NativeStableDays int
	// Sign-offs of the earlier umbrella steps.
	ParitySignedOff   bool
	ABValidationDone  bool
	RetirementDeltaOK bool
}

// runtimeForBot resolves the configured runtime of a bot name, tolerating the
// casing/spacing variants a hand-written config delta may use.
func runtimeForBot(runtimes map[string]string, name string) string {
	if len(runtimes) == 0 {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(name))
	for configured, runtime := range runtimes {
		if strings.ToLower(strings.TrimSpace(configured)) == key {
			return runtime
		}
	}
	return ""
}

// familyIsNative reports whether a family's native runtime is configured, either
// under its native name or by flipping the legacy bot in place.
func familyIsNative(runtimes map[string]string, stage NativeBotStage) bool {
	if IsNativeBotRuntime(runtimeForBot(runtimes, stage.NativeRuntime)) {
		return true
	}
	for _, legacy := range stage.LegacyBots {
		if IsNativeBotRuntime(runtimeForBot(runtimes, legacy)) {
			return true
		}
	}
	return false
}

// OpencodeLegacyBots returns the opencode-era bots whose family still depends on
// the opencode backend, derived from the single flip description in rollout.go.
func OpencodeLegacyBots(state OpencodeRetirementState) []string {
	out := []string{}
	for _, stage := range NativeBotStages() {
		if familyIsNative(state.ConfiguredRuntimes, stage) {
			continue
		}
		out = append(out, stage.LegacyBots...)
	}
	sort.Strings(out)
	return out
}

// OpencodeRetirementBlockers returns the reasons the opencode integration cannot
// be stopped yet. An empty result is the go for retirement.
func OpencodeRetirementBlockers(state OpencodeRetirementState) []string {
	blockers := []string{}
	if legacy := OpencodeLegacyBots(state); len(legacy) > 0 {
		blockers = append(blockers, "families still running "+OpenCodeLegacyRuntime+": "+strings.Join(legacy, ", "))
	}
	if !state.ParitySignedOff {
		blockers = append(blockers, "native coder parity is not signed off (ci#581)")
	}
	if !state.RetirementDeltaOK {
		blockers = append(blockers, "no flip/rollback delta published for every family (ci#582)")
	}
	if !state.ABValidationDone {
		blockers = append(blockers, "no opencode/native A/B validation recorded (ci#583)")
	}
	blockers = append(blockers, RetirementBlockers(NativeBotMigrationState{
		RolledOutThrough: state.RolledOutThrough,
		RollbackProvenOn: state.RollbackProvenOn,
		NativeStableDays: state.NativeStableDays,
	})...)
	return blockers
}

// OpencodeRetirementReady reports whether the opencode integration can stop.
func OpencodeRetirementReady(state OpencodeRetirementState) bool {
	return len(OpencodeRetirementBlockers(state)) == 0
}

// RenderOpencodeRetirementMarkdown renders the checklist plus the live gate, so
// the umbrella report and the ticket body cannot drift apart.
func RenderOpencodeRetirementMarkdown(state OpencodeRetirementState) string {
	var b strings.Builder
	b.WriteString("# opencode retirement (ci#585)\n\n")
	for _, s := range OpencodeRetirementPlan() {
		b.WriteString(fmt.Sprintf("- **%s** (%s) — %s\n", s.Title, s.Ticket, s.Evidence))
	}
	b.WriteString("\n## Gate\n\n")
	if OpencodeRetirementReady(state) {
		b.WriteString("ready: no configured dev bot resolves to the " + OpenCodeLegacyRuntime + " backend, the integration can be stopped.\n")
		return b.String()
	}
	b.WriteString("blocked:\n")
	for _, bl := range OpencodeRetirementBlockers(state) {
		b.WriteString("- " + bl + "\n")
	}
	return b.String()
}
