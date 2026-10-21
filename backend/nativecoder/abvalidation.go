package nativecoder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ci#583 (umbrella ci#585): the A/B validation harness. Before an opencode-era
// bot family is flipped to its native msgmate-coder replacement, the two are
// run side by side on the same tickets and compared axis by axis. The flip is
// gated on that evidence: a family with too few paired runs, an unrecorded
// axis, or a native regression on an axis the legacy run passed may not be
// flipped.
//
// The harness is deliberately data, not automation: the paired runs are
// recorded as ABValidationCase values (from the ticket comments / runbook) and
// the gates below decide from them. Nothing here dispatches a bot or touches
// the deployment.

const (
	// ABValidationMinCasesPerFamily is how many paired runs a family needs
	// before its flip may be scheduled.
	ABValidationMinCasesPerFamily = 3
	// ABValidationEvidenceEnvKey is where the recorded paired-run evidence is
	// pointed at by the runbook tooling.
	ABValidationEvidenceEnvKey = "NATIVE_CODER_AB_VALIDATION"
)

// ABValidationAxis is one comparison dimension of a paired run.
type ABValidationAxis struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// ABValidationAxes returns the comparison axes in a stable order.
func ABValidationAxes() []ABValidationAxis {
	return []ABValidationAxis{
		{ID: "task_completed", Description: "the dispatched ticket is implemented, not just planned"},
		{ID: "pr_opened", Description: "a pull request against main is opened with the applicable CI labels"},
		{ID: "sandbox_used", Description: "the auto-sandbox was provisioned and the repo was edited inside it"},
		{ID: "tool_loop_parity", Description: "the core tool loop ran without a chat_backend fallback"},
		{ID: "auto_continue", Description: "the in-product auto-continue loop finished the turn (ci#537)"},
		{ID: "no_parked_turn", Description: "the bot neither parked mid-turn nor exhausted its tool budget"},
	}
}

func abValidationAxisIDs() []string {
	axes := ABValidationAxes()
	out := make([]string, 0, len(axes))
	for _, axis := range axes {
		out = append(out, axis.ID)
	}
	return out
}

func abValidationAxisKnown(id string) bool {
	for _, axis := range ABValidationAxes() {
		if axis.ID == id {
			return true
		}
	}
	return false
}

// ABValidationCase is one paired run of a legacy bot and its native
// replacement on the same ticket, with the per-axis outcomes of both runs.
type ABValidationCase struct {
	Stage         string          `json:"stage"`
	LegacyBot     string          `json:"legacy_bot"`
	NativeRuntime string          `json:"native_runtime"`
	Ticket        string          `json:"ticket"`
	Legacy        map[string]bool `json:"legacy"`
	Native        map[string]bool `json:"native"`
	Notes         string          `json:"notes,omitempty"`
}

// ABValidationCases is the state ci#583 starts from: no paired run recorded
// yet. It blocks every family, which is the honest starting point.
func ABValidationCases() []ABValidationCase {
	return []ABValidationCase{}
}

// ABValidationTemplate is the fill-in skeleton for the paired runs: one row per
// family and run, with the family/legacy/native pairing pre-filled and the
// outcomes still empty. It is a template, not evidence: ValidateABValidationPlan
// accepts it, but no family is flippable from it because no axis has an outcome.
func ABValidationTemplate() []ABValidationCase {
	out := []ABValidationCase{}
	for _, stage := range NativeBotStages() {
		for i := 1; i <= ABValidationMinCasesPerFamily; i++ {
			out = append(out, ABValidationCase{
				Stage:         stage.Stage,
				LegacyBot:     nativeBotPrimaryLegacy(stage),
				NativeRuntime: stage.NativeRuntime,
				Ticket:        fmt.Sprintf("ci#583 run %d", i),
				Legacy:        map[string]bool{},
				Native:        map[string]bool{},
			})
		}
	}
	return out
}

// ABValidationPassingCases is the green fixture: every axis passed on both
// sides, so only a real regression can block a family. Used by the tests and by
// the report command's "what does a green fleet look like" mode.
func ABValidationPassingCases() []ABValidationCase {
	cases := ABValidationTemplate()
	for i := range cases {
		cases[i].Legacy = map[string]bool{}
		cases[i].Native = map[string]bool{}
		for _, id := range abValidationAxisIDs() {
			cases[i].Legacy[id] = true
			cases[i].Native[id] = true
		}
	}
	return cases
}

func nativeBotPrimaryLegacy(stage NativeBotStage) string {
	if len(stage.LegacyBots) > 0 {
		return stage.LegacyBots[0]
	}
	return ""
}

// ValidateABValidationPlan rejects a plan that cannot gate the migration: an
// unknown family, a family paired against the wrong runtime or a legacy bot
// that does not belong to it, an unknown axis, or a family with no paired run.
func ValidateABValidationPlan(cases []ABValidationCase) error {
	if len(cases) == 0 {
		return fmt.Errorf("ab validation: no paired run recorded for ci#583")
	}
	seen := map[string]bool{}
	for _, c := range cases {
		stage, ok := NativeBotStageByName(c.Stage)
		if !ok {
			return fmt.Errorf("ab validation: unknown family %q", c.Stage)
		}
		if c.NativeRuntime != "" && c.NativeRuntime != stage.NativeRuntime {
			return fmt.Errorf("ab validation: family %q paired against runtime %q, want %q",
				c.Stage, c.NativeRuntime, stage.NativeRuntime)
		}
		if c.LegacyBot != "" && !abValidationLegacyBotMatches(stage, c.LegacyBot) {
			return fmt.Errorf("ab validation: legacy bot %q does not belong to family %q", c.LegacyBot, c.Stage)
		}
		for _, outcomes := range []map[string]bool{c.Legacy, c.Native} {
			for id := range outcomes {
				if !abValidationAxisKnown(id) {
					return fmt.Errorf("ab validation: unknown comparison axis %q", id)
				}
			}
		}
		seen[stage.Stage] = true
	}
	for _, stage := range NativeBotStages() {
		if !seen[stage.Stage] {
			return fmt.Errorf("ab validation: family %q has no paired run", stage.Stage)
		}
	}
	return nil
}

func abValidationLegacyBotMatches(stage NativeBotStage, name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	for _, legacy := range stage.LegacyBots {
		if strings.ToLower(strings.TrimSpace(legacy)) == key {
			return true
		}
	}
	return false
}

// ABValidationFamilyBlockers reports why a family may not be flipped yet: too
// few paired runs, a missing outcome, or a native regression on an axis the
// legacy bot passed.
func ABValidationFamilyBlockers(stage string, cases []ABValidationCase) []string {
	family, ok := NativeBotStageByName(stage)
	if !ok {
		return []string{fmt.Sprintf("ab validation: unknown family %q", stage)}
	}
	out := []string{}
	count := 0
	for _, c := range cases {
		// Match on the family key only: the admin and lead families share a
		// native runtime, so matching on the runtime would count runs twice.
		if !strings.EqualFold(strings.TrimSpace(c.Stage), family.Stage) {
			continue
		}
		count++
		for _, axis := range ABValidationAxes() {
			legacy, legacySet := c.Legacy[axis.ID]
			native, nativeSet := c.Native[axis.ID]
			if !legacySet || !nativeSet {
				out = append(out, fmt.Sprintf("ab validation: family %q case %q: no recorded outcome for axis %q",
					family.Stage, c.Ticket, axis.ID))
				continue
			}
			if legacy && !native {
				out = append(out, fmt.Sprintf("ab validation: family %q case %q: native regressed on axis %q (legacy passed)",
					family.Stage, c.Ticket, axis.ID))
			}
		}
	}
	if count < ABValidationMinCasesPerFamily {
		out = append(out, fmt.Sprintf("ab validation: family %q has %d paired run(s), want at least %d",
			family.Stage, count, ABValidationMinCasesPerFamily))
	}
	sort.Strings(out)
	return abValidationDedupe(out)
}

func abValidationDedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for i, line := range in {
		if i > 0 && in[i-1] == line {
			continue
		}
		out = append(out, line)
	}
	return out
}

// ABValidationEvidenceBlockers is the per-family paired-run gate without the
// fleet-wide ci#581 parity gaps: it answers "is the side-by-side evidence for
// the whole fleet complete and regression-free?".
func ABValidationEvidenceBlockers(cases []ABValidationCase) []string {
	out := []string{}
	if err := ValidateABValidationPlan(cases); err != nil {
		out = append(out, err.Error())
	}
	for _, stage := range NativeBotStages() {
		out = append(out, ABValidationFamilyBlockers(stage.Stage, cases)...)
	}
	sort.Strings(out)
	return abValidationDedupe(out)
}

// ABValidationBlockers is the migration-wide gate: the evidence must be
// complete and regression-free for every family, and the ci#394 parity gaps
// must be closed (ci#581).
func ABValidationBlockers(cases []ABValidationCase) []string {
	out := ABValidationEvidenceBlockers(cases)
	out = append(out, RetirementParityBlockers()...)
	sort.Strings(out)
	return abValidationDedupe(out)
}

// ABValidationFlipAllowed reports whether the evidence for one family is
// complete and regression-free. The ci#581 parity gaps are reported separately
// by ABValidationBlockers because they gate the whole fleet, not one family.
func ABValidationFlipAllowed(stage string, cases []ABValidationCase) bool {
	if _, ok := NativeBotStageByName(stage); !ok {
		return false
	}
	return len(ABValidationFamilyBlockers(stage, cases)) == 0
}

// ABValidatedFamilies lists the families that passed their A/B validation.
func ABValidatedFamilies(cases []ABValidationCase) []string {
	out := []string{}
	for _, stage := range NativeBotStages() {
		if ABValidationFlipAllowed(stage.Stage, cases) {
			out = append(out, stage.Stage)
		}
	}
	sort.Strings(out)
	return out
}

// ABValidationSignOff derives the ci#583 sign-off (the ABValidationDone field of
// the ci#585 retirement gate) from the recorded evidence, so the umbrella flag
// is a fact about the paired runs instead of a hand-set boolean.
func ABValidationSignOff(cases []ABValidationCase) (bool, []string) {
	blockers := ABValidationEvidenceBlockers(cases)
	return len(blockers) == 0, blockers
}

// ABValidatedRetirementState returns the retirement state with the ci#583
// sign-off derived from the A/B evidence. All other fields are passed through
// unchanged, so the caller keeps control of the deployment facts.
func ABValidatedRetirementState(state OpencodeRetirementState, cases []ABValidationCase) OpencodeRetirementState {
	done, _ := ABValidationSignOff(cases)
	state.ABValidationDone = done
	return state
}

// ABValidationReport is the machine-readable result of the harness.
type ABValidationReport struct {
	Umbrella          string             `json:"umbrella"`
	Ticket            string             `json:"ticket"`
	EvidenceEnvKey    string             `json:"evidence_env_key"`
	MinCasesPerFamily int                `json:"min_cases_per_family"`
	Axes              []ABValidationAxis `json:"axes"`
	Cases             []ABValidationCase `json:"cases"`
	ValidatedFamilies []string           `json:"validated_families"`
	FlipAllowed       map[string]bool    `json:"flip_allowed"`
	SignOff           bool               `json:"sign_off"`
	Blockers          []string           `json:"blockers"`
}

// ABValidationReportFor assembles the report of a paired-run set.
func ABValidationReportFor(cases []ABValidationCase) ABValidationReport {
	if cases == nil {
		cases = []ABValidationCase{}
	}
	allowed := map[string]bool{}
	for _, stage := range NativeBotStages() {
		allowed[stage.Stage] = ABValidationFlipAllowed(stage.Stage, cases)
	}
	validated := ABValidatedFamilies(cases)
	if validated == nil {
		validated = []string{}
	}
	blockers := ABValidationBlockers(cases)
	if blockers == nil {
		blockers = []string{}
	}
	signOff, _ := ABValidationSignOff(cases)
	return ABValidationReport{
		Umbrella:          "ci#585",
		Ticket:            "ci#583",
		EvidenceEnvKey:    ABValidationEvidenceEnvKey,
		MinCasesPerFamily: ABValidationMinCasesPerFamily,
		Axes:              ABValidationAxes(),
		Cases:             cases,
		ValidatedFamilies: validated,
		FlipAllowed:       allowed,
		SignOff:           signOff,
		Blockers:          blockers,
	}
}

// ABValidationJSON renders the harness result for the runbook tooling.
func ABValidationJSON(cases []ABValidationCase) (string, error) {
	raw, err := json.MarshalIndent(ABValidationReportFor(cases), "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// RenderABValidationMarkdown renders the side-by-side table used by the runbook.
func RenderABValidationMarkdown(cases []ABValidationCase) string {
	var b strings.Builder
	b.WriteString("# opencode/native A/B validation (ci#583)\n\n")
	b.WriteString("| family | legacy bot | native runtime | paired runs | flip allowed |\n")
	b.WriteString("|--------|-----------|----------------|-------------|--------------|\n")
	for _, stage := range NativeBotStages() {
		count := 0
		for _, c := range cases {
			if strings.EqualFold(strings.TrimSpace(c.Stage), stage.Stage) {
				count++
			}
		}
		allowed := "no"
		if ABValidationFlipAllowed(stage.Stage, cases) {
			allowed = "yes"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d/%d | %s |\n",
			stage.Stage, nativeBotPrimaryLegacy(stage), stage.NativeRuntime, count, ABValidationMinCasesPerFamily, allowed)
	}
	blockers := ABValidationBlockers(cases)
	if len(blockers) > 0 {
		b.WriteString("\nBlockers:\n")
		for _, blocker := range blockers {
			fmt.Fprintf(&b, "- %s\n", blocker)
		}
	}
	return b.String()
}

// Migration umbrella step statuses.
const (
	MigrationStepSignedOff = "signed-off"
	MigrationStepPending   = "pending"
	MigrationStepBlocked   = "blocked"
)

// MigrationUmbrellaStep is one checklist item of the ci#585 umbrella with the
// status the repository can actually support.
type MigrationUmbrellaStep struct {
	ID     string `json:"id"`
	Ticket string `json:"ticket"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// MigrationUmbrellaSteps derives the ci#585 checklist from the artifacts that
// are already on main, so the umbrella ticket cannot claim progress the repo
// does not support. Ticket ids and titles come from OpencodeRetirementPlan, so
// the checklist cannot drift from the retirement gate.
func MigrationUmbrellaSteps(cases []ABValidationCase) []MigrationUmbrellaStep {
	out := []MigrationUmbrellaStep{}
	for _, step := range OpencodeRetirementPlan() {
		out = append(out, migrationUmbrellaStepFor(step, cases))
	}
	return out
}

func migrationUmbrellaStepFor(step OpencodeRetirementStep, cases []ABValidationCase) MigrationUmbrellaStep {
	s := MigrationUmbrellaStep{
		ID:     step.ID,
		Ticket: step.Ticket,
		Title:  step.Title,
		Status: MigrationStepPending,
		Detail: step.Evidence,
	}
	switch step.ID {
	case "parity_signoff":
		if gaps := RetirementParityBlockers(); len(gaps) > 0 {
			s.Status = MigrationStepBlocked
			s.Detail = fmt.Sprintf("%d opencode parity gap(s) of ci#394 are still open: %s",
				len(gaps), strings.Join(gaps, "; "))
		} else {
			s.Status = MigrationStepSignedOff
			s.Detail = "the ci#394 parity registry reports no open capability gap"
		}
	case "config_delta":
		if err := ValidateOpencodeRetirementPlan(); err != nil {
			s.Status = MigrationStepBlocked
			s.Detail = err.Error()
			break
		}
		s.Detail = fmt.Sprintf(
			"%d flip/rollback step(s) render from the flip registry; publish through publish-open-chat-config.yaml with DRY_RUN=true first",
			len(OpencodeRetirementPlan()))
	case "ab_validation":
		blockers := ABValidationEvidenceBlockers(cases)
		if len(blockers) > 0 {
			s.Status = MigrationStepBlocked
			s.Detail = blockers[0]
			break
		}
		s.Status = MigrationStepSignedOff
		s.Detail = fmt.Sprintf("%d/%d family/families validated side-by-side",
			len(ABValidatedFamilies(cases)), len(NativeBotStages()))
	case "rollout":
		s.Detail = fmt.Sprintf(
			"flip bot by bot, keep %s as the rollback target for %d day(s); the native lead is last",
			OpenCodeLegacyRuntime, NativeBotRollbackWindowDays)
	case "retire_opencode":
		s.Detail = fmt.Sprintf(
			"stop the %s bots/integration once no configured dev bot resolves to %s",
			OpenCodeLegacyRuntime, OpenCodeLegacyRuntime)
	}
	return s
}

// MigrationUmbrellaReady reports whether every checklist item is signed off.
func MigrationUmbrellaReady(steps []MigrationUmbrellaStep) bool {
	if len(steps) == 0 {
		return false
	}
	for _, step := range steps {
		if step.Status != MigrationStepSignedOff {
			return false
		}
	}
	return true
}

// RenderMigrationUmbrellaMarkdown renders the umbrella checklist.
func RenderMigrationUmbrellaMarkdown(steps []MigrationUmbrellaStep) string {
	var b strings.Builder
	b.WriteString("| ticket | step | status | detail |\n")
	b.WriteString("|--------|------|--------|--------|\n")
	for _, step := range steps {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", step.Ticket, step.Title, step.Status, step.Detail)
	}
	return b.String()
}
