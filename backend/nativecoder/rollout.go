package nativecoder

import (
	"fmt"
	"sort"
	"strings"

	"backend/runtimecfg"
)

// rollout.go is the machine-checked registry of the native-bot migration of
// ci#584: rolling the native msgmate_coder runtimes into the ci.msgmate.io
// development loop family by family (plan, then repl/coding, then review, then
// admin, then the coordinating lead last) with an open rollback window, and then
// retiring the opencode integration.
//
// It is the single source of truth for the flip: which native runtime replaces
// which opencode-era bot, in which order the families move, which monitoring
// channel proves a flip, which legacy runtime stays configured but out of
// rotation while a rollback window is open, and what still blocks dropping
// opencode from the deployment profile. The package is data plus pure functions
// - no network, no cluster access, no credentials - so the whole migration is
// unit tested here and the private runbook is rendered from the same registry by
// RenderNativeBotRolloutMarkdown and NativeBotStageChecklist.
//
// The native runtime names mirror the selector constants of
// backend/api/msgmate/runtime_selector_routing.go; the msgmate package derives
// its legacy alias map from LegacyRuntimeBotAliases, so the flip is declared
// exactly once.

// Native runtime names of the msgmate_coder family.
const (
	NativeBotPlanRuntime   = "msgmate-coder-plan-bot"
	NativeBotReplRuntime   = "msgmate-coder-repl-bot"
	NativeBotReviewRuntime = "msgmate-coder-review-bot"
	NativeBotLeadRuntime   = "msgmate-coder-development-lead"
	// NativeBotAdminRuntime is the admin family; it shares the coordinating
	// lead runtime, so the admin flip must land before the lead flip.
	NativeBotAdminRuntime = NativeBotLeadRuntime
)

// OpenCodeLegacyRuntime is the integration retired once every family runs
// native and stable.
const OpenCodeLegacyRuntime = "opencode"

// Rollback / retirement policy knobs.
const (
	// NativeBotRollbackWindowDays is how long the legacy runtime stays
	// configured but out of rotation after a family flips.
	NativeBotRollbackWindowDays = 7
	// NativeBotStabilityWindowDays is how long the whole fleet must have run
	// native before the legacy integration is dropped from the profile.
	NativeBotStabilityWindowDays = 14
	// NativeBotRollbackWindowEnvKey lists the stages whose rollback window is
	// open (comma separated stage names, native runtime names, legacy bot names
	// or "all"), so a rollout can be paused or rolled back without a rebuild.
	NativeBotRollbackWindowEnvKey = "MSGMATECODER_NATIVE_ROLLBACK_WINDOW"
)

// Family keys of the flip, in flip order.
const (
	stagePlan   = "plan"
	stageRepl   = "repl"
	stageReview = "review"
	stageAdmin  = "admin"
	stageLead   = "lead"
)

// NativeBotStage is one family of the flip.
type NativeBotStage struct {
	// Stage is the family key used in the runbook and in the rollback-window
	// setting.
	Stage string
	// Order is the flip position; the lead is always last.
	Order int
	// NativeRuntime is the native msgmate_coder runtime that takes over.
	NativeRuntime string
	// LegacyBots are the opencode-era bot names the family replaces.
	LegacyBots []string
	// Monitors are the signals watched after the flip.
	Monitors []string
	// Critical marks a family that must never carry the rollback proof.
	Critical bool
}

// NativeBotStages returns the flip order. The lead flips last: everything the
// fleet needs (plan, repl/coding, review and admin) has to be native and stable
// before the coordinating bot moves, so a bad lead can always be rolled back
// onto a fleet that still works.
func NativeBotStages() []NativeBotStage {
	return []NativeBotStage{
		{Stage: stagePlan, Order: 1, NativeRuntime: NativeBotPlanRuntime,
			LegacyBots: []string{"issue-plan-bot"}, Monitors: nativeBotMonitors()},
		{Stage: stageRepl, Order: 2, NativeRuntime: NativeBotReplRuntime,
			LegacyBots: []string{"issue-repl-bot", "coding-bot", "opencode", "opencode_select_project-bot"},
			Monitors:   nativeBotMonitors()},
		{Stage: stageReview, Order: 3, NativeRuntime: NativeBotReviewRuntime,
			LegacyBots: []string{"issue-review-bot", "review-bot"}, Monitors: nativeBotMonitors()},
		{Stage: stageAdmin, Order: 4, NativeRuntime: NativeBotAdminRuntime,
			LegacyBots: []string{"open-chat-go-ci-admin"}, Monitors: nativeBotMonitors()},
		{Stage: stageLead, Order: 5, NativeRuntime: NativeBotLeadRuntime,
			LegacyBots: []string{"open-chat-development-lead"}, Monitors: nativeBotMonitors(),
			Critical: true},
	}
}

// nativeBotMonitors are the post-flip signals of a stage: the live runtime
// canary and the log monitoring of the ci deployment.
func nativeBotMonitors() []string {
	return []string{"live-runtime-canary", "log-monitoring"}
}

// IsNativeBotRuntime reports whether a bot name is a native msgmate_coder
// runtime.
func IsNativeBotRuntime(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "msgmate-coder")
}

// LegacyRuntimeBotAliases maps every opencode-era bot name onto the native
// runtime that replaces it. The msgmate runtime-selector routing derives its
// alias map from this function, so a family is retired in exactly one place.
func LegacyRuntimeBotAliases() map[string]string {
	aliases := map[string]string{}
	for _, stage := range NativeBotStages() {
		for _, legacy := range stage.LegacyBots {
			aliases[strings.ToLower(strings.TrimSpace(legacy))] = stage.NativeRuntime
		}
	}
	return aliases
}

// NativeBotStageForLegacyBot returns the stage that owns an opencode-era bot.
func NativeBotStageForLegacyBot(name string) (NativeBotStage, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	for _, stage := range NativeBotStages() {
		for _, legacy := range stage.LegacyBots {
			if strings.ToLower(strings.TrimSpace(legacy)) == key {
				return stage, true
			}
		}
	}
	return NativeBotStage{}, false
}

// NativeBotStageForRuntime returns the stage that owns a native runtime name.
func NativeBotStageForRuntime(runtime string) (NativeBotStage, bool) {
	key := strings.TrimSpace(runtime)
	for _, stage := range NativeBotStages() {
		if stage.NativeRuntime == key {
			return stage, true
		}
	}
	return NativeBotStage{}, false
}

// NativeBotStageByName resolves a family by stage name, native runtime name or
// legacy bot name - the three spellings the rollback-window setting accepts.
func NativeBotStageByName(name string) (NativeBotStage, bool) {
	key := strings.TrimSpace(name)
	for _, stage := range NativeBotStages() {
		if strings.EqualFold(stage.Stage, key) || stage.NativeRuntime == key {
			return stage, true
		}
	}
	return NativeBotStageForLegacyBot(key)
}

// NativeBotRollbackStages reports which stages currently have their rollback
// window open; there the legacy runtime stays configured but out of rotation. An
// empty or unknown value closes every window.
func NativeBotRollbackStages() map[string]bool {
	open := map[string]bool{}
	raw := strings.TrimSpace(runtimecfg.GetAll()[NativeBotRollbackWindowEnvKey].Value)
	if raw == "" {
		return open
	}
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.EqualFold(token, "all") {
			for _, stage := range NativeBotStages() {
				open[stage.Stage] = true
			}
			return open
		}
		if stage, ok := NativeBotStageByName(token); ok {
			open[stage.Stage] = true
		}
	}
	return open
}

// NativeBotRotation returns the runtime serving a stage once the rollout has
// reached rolledOutThrough: "native" for every flipped family, the legacy
// runtime for the rest. A flipped family with an open rollback window keeps
// serving native - the legacy runtime is only the recorded rollback target.
func NativeBotRotation(stage NativeBotStage, rolledOutThrough string) string {
	if nativeBotRolledOut(stage, rolledOutThrough) {
		return "native"
	}
	return OpenCodeLegacyRuntime
}

// NativeBotRollbackTarget returns the legacy runtime a stage falls back to.
func NativeBotRollbackTarget(stage NativeBotStage) string {
	return OpenCodeLegacyRuntime
}

func nativeBotRolledOut(stage NativeBotStage, rolledOutThrough string) bool {
	through, ok := NativeBotStageByName(rolledOutThrough)
	if !ok {
		return false
	}
	return stage.Order <= through.Order
}

// NativeBotMigrationState records the observed rollout progress of the ci
// deployment. The values are evidence, not configuration: they are filled in
// from the runbook after each real dispatch.
type NativeBotMigrationState struct {
	// RolledOutThrough is the last flipped family (empty = nothing flipped yet).
	RolledOutThrough string
	// RollbackProvenOn is the family a real rollback was executed on.
	RollbackProvenOn string
	// NativeStableDays is how long the whole fleet has run native.
	NativeStableDays int
	// RetainedParity explains any family kept on the legacy runtime because of a
	// parity gap; an empty reason is a blocker.
	RetainedParity map[string]string
}

// RetirementBlockers lists why the legacy integration may not be dropped from
// the deployment profile yet. An empty result is the go for retirement.
func RetirementBlockers(state NativeBotMigrationState) []string {
	blockers := []string{}
	through, recorded := NativeBotStageByName(state.RolledOutThrough)
	if !recorded {
		return []string{"no family recorded as rolled out: retirement needs every family flipped"}
	}
	for _, stage := range NativeBotStages() {
		if stage.Order > through.Order {
			blockers = append(blockers, fmt.Sprintf("family %q still runs %s: flip it first", stage.Stage, OpenCodeLegacyRuntime))
		}
	}
	if !RollbackProvenOnNonCriticalStage(state.RollbackProvenOn) {
		blockers = append(blockers, "rollback was never proven on a non-critical family")
	}
	if state.NativeStableDays < NativeBotStabilityWindowDays {
		blockers = append(blockers, fmt.Sprintf("fleet has been native for %d day(s), need %d", state.NativeStableDays, NativeBotStabilityWindowDays))
	}
	for family, reason := range state.RetainedParity {
		if strings.TrimSpace(reason) == "" {
			blockers = append(blockers, fmt.Sprintf("parity exception for %q has no recorded reason", family))
		}
	}
	sort.Strings(blockers)
	return blockers
}

// RetirementReadiness is the full go/no-go for dropping the legacy integration
// from the deployment profile: the rollout mechanics of RetirementBlockers
// ("did every family flip, was a rollback proven, is the fleet stable") plus the
// ci#394 capability gate in RetirementParityBlockers ("is the native runtime
// capable enough to be the only one"). RetirementBlockers alone is not enough:
// it can be empty while the coder still misses opencode capabilities.
func RetirementReadiness(state NativeBotMigrationState) []string {
	blockers := RetirementBlockers(state)
	blockers = append(blockers, RetirementParityBlockers()...)
	sort.Strings(blockers)
	return blockers
}

// ParityRetentionEntries derives the RetainedParity entries of
// NativeBotMigrationState from the ci#394 gap registry: while capability gaps
// are open, every family that would otherwise be retired needs a recorded
// reason, and the reason is not hand-written. An empty map is the state where
// no family needs a parity exception because the registry is closed.
func ParityRetentionEntries() map[string]string {
	reasons := RetirementParityBlockers()
	if len(reasons) == 0 {
		return map[string]string{}
	}
	reason := fmt.Sprintf("retained on %s: %d opencode parity gap(s) of ci#394 are still open (%s)",
		OpenCodeLegacyRuntime, len(reasons), firstParityBlockerID(reasons))
	out := map[string]string{}
	for _, stage := range NativeBotStages() {
		out[stage.Stage] = reason
	}
	return out
}

// firstParityBlockerID extracts the gap id from a blocker line so the derived
// retention reason names a concrete gap. The blockers are sorted, so the result
// is stable.
func firstParityBlockerID(blockers []string) string {
	if len(blockers) == 0 {
		return ""
	}
	line := blockers[0]
	start := strings.Index(line, "\"")
	if start < 0 {
		return line
	}
	rest := line[start+1:]
	if end := strings.Index(rest, "\""); end >= 0 {
		return rest[:end]
	}
	return rest
}

// RollbackProofEligible reports whether a family may carry the rollback proof
// (ci#584: the rollback must be proven at least once on a non-critical bot). A
// family is eligible when it is not the coordinating lead and does not share the
// coordinating runtime, so proving a rollback never means rolling back the bot
// that coordinates the fleet.
func RollbackProofEligible(stage string) bool {
	family, ok := NativeBotStageByName(stage)
	if !ok {
		return false
	}
	if family.Critical || family.NativeRuntime == NativeBotLeadRuntime {
		return false
	}
	return true
}

// RollbackProvenOnNonCriticalStage reports whether the recorded rollback proof
// came from a family that is safe to roll back (ci#584: proven at least once on
// a non-critical bot).
func RollbackProvenOnNonCriticalStage(stage string) bool {
	return RollbackProofEligible(stage)
}

// NativeBotStageChecklist is the per-family procedure of a flip: flip, watch,
// dispatch for real, record the runtime mapping and keep the rollback target.
func NativeBotStageChecklist(stage NativeBotStage) []string {
	return []string{
		fmt.Sprintf("flip family %q to the native runtime %s", stage.Stage, stage.NativeRuntime),
		fmt.Sprintf("watch %s and confirm no regression", strings.Join(stage.Monitors, " + ")),
		"run one real dispatch and confirm the native bot does not park, opens a PR and uses the sandbox",
		fmt.Sprintf("record the runtime mapping (%s -> %s) in the runbook", stage.Stage, stage.NativeRuntime),
		fmt.Sprintf("keep %s configured but out of rotation for the %d day rollback window; rollback target: %s",
			OpenCodeLegacyRuntime, NativeBotRollbackWindowDays, NativeBotRollbackTarget(stage)),
	}
}

// NativeBotRotationExports renders the rollout table used by the runbook.
func NativeBotRotationExports() string {
	return RenderNativeBotRolloutMarkdown()
}

// RenderNativeBotRolloutMarkdown renders the rollout table used by the runbook.
func RenderNativeBotRolloutMarkdown() string {
	var b strings.Builder
	b.WriteString("| # | family | native runtime | legacy bots | legacy runtime |\n")
	b.WriteString("|---|--------|----------------|-------------|----------------|\n")
	for _, stage := range NativeBotStages() {
		runtime := OpenCodeLegacyRuntime
		if stage.Critical {
			runtime = OpenCodeLegacyRuntime + " (rollback of the lead is gated: proof must come from a non-critical family)"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n",
			stage.Order, stage.Stage, stage.NativeRuntime, strings.Join(stage.LegacyBots, ", "), runtime)
	}
	return b.String()
}
