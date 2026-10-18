package nativecoder

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// devlead.go implements the development-lead side of the CI-error relay
// (ci#608): ingest a structured CI-failure event, resolve the repo's live +
// recent development-agent interactions, pick the interaction that owns the
// failing change and route the failure there — appending to an ongoing
// interaction, re-activating a past one, or dispatching a fresh plan->implement
// session — while obeying the fix-cycle cap, the "exactly one fix attempt per
// relayed failure" dedupe rule and the "never poll CI" rule.
//
// The package deliberately stays free of integration imports (see policy.go):
// the relay transport is the base-integration abstraction (ci#607), while the
// lead's ingest + routing decision is pure, deterministic and unit-testable
// here.
//
// Usage:
//
//	event, err := nativecoder.ParseCIFailureEvent(raw)
//	decision, err := nativecoder.DevLeadRouter{}.Route(event, candidates)
//	// decision.Route decides append / reactivate / spawn / dedupe / escalate
//	// and decision.Prompt(event) is the structured re-prompt to deliver.

const (
	// CIFailureEventKind is the relay event kind the lead consumes.
	CIFailureEventKind = "ci_failure"
	// CIFailureEventAliasKind is accepted as an alias (the earlier draft
	// spelling of the same event).
	CIFailureEventAliasKind = "ci_error"
	// CIFailureSchemaVersion is the payload contract version this package
	// understands. A payload advertising a newer version is rejected instead of
	// being misinterpreted.
	CIFailureSchemaVersion = 1
	// DefaultFixCycleMaxAttempts mirrors the CI-side `CI_FIX_MAX_ATTEMPTS`:
	// after this many fix attempts for one fix session the lead stops
	// dispatching and flags the failure for maintainer intervention.
	DefaultFixCycleMaxAttempts = 3
	// DefaultDevLeadRecentWindow bounds which past interactions a lead treats as
	// "recent". An active interaction is always considered; a past one only
	// inside this window.
	DefaultDevLeadRecentWindow = 24 * time.Hour

	// NeverPollCINote is embedded in every rendered fix prompt so the rule
	// travels with the instruction: the agent must not poll or wait on CI.
	NeverPollCINote = "Do not poll or wait on CI: push the fix and stop. The lead reacts to the next relayed CI event."
	// CIFailureFixMarker marks a relayed CI-fix request so the lead can dedupe
	// against already-open fix sessions.
	CIFailureFixMarker = "<!-- oci-ci-failure-fix -->"
)

// Interaction status values the lead understands. "active" means the
// interaction is running and can be appended to; everything else is treated as
// a past interaction that may be reactivated.
const (
	LeadInteractionActive    = "active"
	LeadInteractionIdle      = "idle"
	LeadInteractionCompleted = "completed"
	LeadInteractionFailed    = "failed"
)

// CIFailureEvent is the small, versioned, secret-free payload a CI system hands
// to the development lead (ci#607 contract, consumed here). All fields except
// the repo are optional; the fix session is keyed by the most specific target
// available (PR, else branch, else commit).
type CIFailureEvent struct {
	SchemaVersion int      `json:"schema_version,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Repo          string   `json:"repo"`
	PRNumber      int      `json:"pr_number,omitempty"`
	Branch        string   `json:"branch,omitempty"`
	Sha           string   `json:"sha,omitempty"`
	RunURL        string   `json:"run_url,omitempty"`
	FailingJobs   []string `json:"failing_jobs,omitempty"`
	FailureLogs   string   `json:"failure_logs,omitempty"`
	Attempt       int      `json:"attempt,omitempty"`
	Severity      string   `json:"severity,omitempty"`
	OccurredAt    string   `json:"occurred_at,omitempty"`
}

// Validate rejects an event the lead must not route (no repo, unknown kind, a
// schema version it cannot understand).
func (e CIFailureEvent) Validate() error {
	if NormalizeRepo(e.Repo) == "" {
		return errors.New("ci_failure event is missing repo")
	}
	if e.SchemaVersion < 0 {
		return fmt.Errorf("invalid ci_failure schema_version %d", e.SchemaVersion)
	}
	if e.SchemaVersion > CIFailureSchemaVersion {
		return fmt.Errorf("unsupported ci_failure schema_version %d (max %d)", e.SchemaVersion, CIFailureSchemaVersion)
	}
	if kind := strings.ToLower(strings.TrimSpace(e.Kind)); kind != "" && kind != CIFailureEventKind && kind != CIFailureEventAliasKind {
		return fmt.Errorf("unsupported event kind %q", e.Kind)
	}
	if e.PRNumber < 0 {
		return fmt.Errorf("invalid pr_number %d", e.PRNumber)
	}
	if e.Attempt < 0 {
		return fmt.Errorf("invalid attempt %d", e.Attempt)
	}
	return nil
}

// ParseCIFailureEvent decodes and validates a relayed CI-failure payload.
func ParseCIFailureEvent(raw []byte) (CIFailureEvent, error) {
	var event CIFailureEvent
	if len(raw) == 0 {
		return event, errors.New("empty ci_failure payload")
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return event, fmt.Errorf("invalid ci_failure payload: %w", err)
	}
	if err := event.Validate(); err != nil {
		return event, err
	}
	return event, nil
}

// FixSessionKey is the dedupe/attempt-cap identity of a failure: one fix
// session per repo+PR, else repo+branch, else repo+commit.
func (e CIFailureEvent) FixSessionKey() string {
	repo := NormalizeRepo(e.Repo)
	switch {
	case e.PRNumber > 0:
		return fmt.Sprintf("%s#pr-%d", repo, e.PRNumber)
	case NormalizeBranch(e.Branch) != "":
		return fmt.Sprintf("%s#branch-%s", repo, NormalizeBranch(e.Branch))
	default:
		return fmt.Sprintf("%s#sha-%s", repo, strings.ToLower(strings.TrimSpace(e.Sha)))
	}
}

func (e CIFailureEvent) kindOrDefault() string {
	if kind := strings.TrimSpace(e.Kind); kind != "" {
		return kind
	}
	return CIFailureEventKind
}

func (e CIFailureEvent) providerOrDefault() string {
	if provider := strings.TrimSpace(e.Provider); provider != "" {
		return provider
	}
	return "unknown"
}

// NormalizeRepo lower-cases a repo slug and drops a `.git` suffix so
// `Owner/Repo.git` and `owner/repo` are the same target.
func NormalizeRepo(repo string) string {
	normalized := strings.ToLower(strings.TrimSpace(repo))
	return strings.TrimSuffix(normalized, ".git")
}

// NormalizeBranch lower-cases a branch and strips the `refs/heads/` prefix.
func NormalizeBranch(branch string) string {
	normalized := strings.ToLower(strings.TrimSpace(branch))
	return strings.TrimPrefix(normalized, "refs/heads/")
}

// LeadInteraction is the lead's read-only view of a candidate development-agent
// interaction as retrieved from the chat/interaction store.
type LeadInteraction struct {
	UUID           string
	Kind           string
	Repo           string
	Branch         string
	PRNumber       int
	Sha            string
	Status         string
	LastActivityAt time.Time
	// FixAttempts records how many fix attempts were already attributed to this
	// interaction per fix session key (see FixSessionKey). A non-zero entry also
	// means the interaction owns that fix session.
	FixAttempts map[string]int
}

// IsActive reports whether the interaction is running (appendable).
func (i LeadInteraction) IsActive() bool {
	return strings.EqualFold(strings.TrimSpace(i.Status), LeadInteractionActive)
}

// RecordedAttempts returns how many fix attempts are already attributed to this
// interaction for a fix session key.
func (i LeadInteraction) RecordedAttempts(fixSessionKey string) int {
	if i.FixAttempts == nil || fixSessionKey == "" {
		return 0
	}
	return i.FixAttempts[fixSessionKey]
}

// MatchesTarget reports whether the interaction last touched the failing
// PR/branch/commit.
func (i LeadInteraction) MatchesTarget(event CIFailureEvent) bool {
	if event.PRNumber > 0 && i.PRNumber > 0 && event.PRNumber == i.PRNumber {
		return true
	}
	if branch := NormalizeBranch(event.Branch); branch != "" && branch == NormalizeBranch(i.Branch) {
		return true
	}
	sha := strings.ToLower(strings.TrimSpace(event.Sha))
	return sha != "" && sha == strings.ToLower(strings.TrimSpace(i.Sha))
}

// LeadRouteKind is the decision the lead took for a relayed CI failure.
type LeadRouteKind string

const (
	// RouteAppendOngoing appends the structured failure to the repo's running
	// development-agent interaction (re-prompting it to fix).
	RouteAppendOngoing LeadRouteKind = "append_ongoing"
	// RouteReactivatePast hands the failure to a past interaction that
	// produced the failing change (re-prompted to continue on its branch).
	RouteReactivatePast LeadRouteKind = "reactivate_past"
	// RouteSpawnNew dispatches a fresh plan->implement session on the failing
	// branch because no interaction owns it.
	RouteSpawnNew LeadRouteKind = "spawn_new"
	// RouteDedupeExisting makes no new fix attempt: an already-open fix session
	// for the same fix session key is handling this run.
	RouteDedupeExisting LeadRouteKind = "dedupe_existing"
	// RouteEscalateMaintainer flags the failure for a human: the fix-cycle cap
	// is exhausted.
	RouteEscalateMaintainer LeadRouteKind = "escalate_maintainer"
)

// LeadRoutingDecision is the lead's answer for one relayed CI failure, including
// the visible trail of why the interaction was chosen.
type LeadRoutingDecision struct {
	Route                 LeadRouteKind
	Repo                  string
	FixSessionKey         string
	Attempt               int
	MaxAttempts           int
	TargetInteractionUUID string
	TargetInteractionKind string
	Reason                string
	Trail                 []string
}

// Dispatch reports whether the decision starts (or continues) a fix attempt.
// Dedupe and escalation deliberately do not dispatch anything.
func (d LeadRoutingDecision) Dispatch() bool {
	return d.Route == RouteAppendOngoing || d.Route == RouteReactivatePast || d.Route == RouteSpawnNew
}

func (d *LeadRoutingDecision) trailf(format string, args ...interface{}) {
	d.Trail = append(d.Trail, fmt.Sprintf(format, args...))
}

// Prompt renders the structured fix request (with the routing trail) that is
// appended to the chosen interaction. It carries the marker used for dedupe and
// the never-poll rule.
func (d LeadRoutingDecision) Prompt(event CIFailureEvent) string {
	var b strings.Builder
	b.WriteString(CIFailureFixMarker + "\n")
	b.WriteString("## Relayed CI failure\n\n")
	fmt.Fprintf(&b, "- repo: `%s`\n", NormalizeRepo(event.Repo))
	if event.PRNumber > 0 {
		fmt.Fprintf(&b, "- pull request: #%d\n", event.PRNumber)
	}
	if branch := NormalizeBranch(event.Branch); branch != "" {
		fmt.Fprintf(&b, "- branch: `%s`\n", branch)
	}
	if sha := strings.TrimSpace(event.Sha); sha != "" {
		fmt.Fprintf(&b, "- commit: `%s`\n", sha)
	}
	if runURL := strings.TrimSpace(event.RunURL); runURL != "" {
		fmt.Fprintf(&b, "- run: %s\n", runURL)
	}
	if severity := strings.TrimSpace(event.Severity); severity != "" {
		fmt.Fprintf(&b, "- severity: %s\n", severity)
	}
	fmt.Fprintf(&b, "- fix session: `%s`\n- fix attempt: %d/%d\n", d.FixSessionKey, d.Attempt, d.MaxAttempts)
	if len(event.FailingJobs) > 0 {
		fmt.Fprintf(&b, "- failing jobs: %s\n", strings.Join(event.FailingJobs, ", "))
	}
	if logs := strings.TrimSpace(event.FailureLogs); logs != "" {
		b.WriteString("\n### Failure output\n\n```\n")
		b.WriteString(logs)
		if !strings.HasSuffix(logs, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}
	b.WriteString("\n### Routing decision\n\n")
	fmt.Fprintf(&b, "- route: `%s`\n", d.Route)
	if d.TargetInteractionUUID != "" {
		fmt.Fprintf(&b, "- target interaction: `%s` (%s)\n", d.TargetInteractionUUID, d.TargetInteractionKind)
	}
	fmt.Fprintf(&b, "- why: %s\n", d.Reason)
	if len(d.Trail) > 0 {
		b.WriteString("\n### Trail\n\n")
		for _, line := range d.Trail {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	b.WriteString("\n" + NeverPollCINote + "\n")
	return b.String()
}

// DevLeadRouter decides where a relayed CI failure goes. The zero value uses the
// defaults documented above.
type DevLeadRouter struct {
	// MaxAttempts is the fix-cycle cap per fix session (default
	// DefaultFixCycleMaxAttempts).
	MaxAttempts int
	// RecentWindow bounds which past interactions are candidates (default
	// DefaultDevLeadRecentWindow).
	RecentWindow time.Duration
	// Now is injectable for tests (default time.Now).
	Now func() time.Time
}

func (r DevLeadRouter) maxAttempts() int {
	if r.MaxAttempts > 0 {
		return r.MaxAttempts
	}
	return DefaultFixCycleMaxAttempts
}

func (r DevLeadRouter) recentWindow() time.Duration {
	if r.RecentWindow > 0 {
		return r.RecentWindow
	}
	return DefaultDevLeadRecentWindow
}

func (r DevLeadRouter) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Route ingests a relayed CI-failure event and returns the routing decision for
// the repo's interactions. The order is deliberate:
//
//  1. dedupe — an already-open fix session for this fix session key wins, so one
//     relayed failure never starts a second fix attempt;
//  2. fix-cycle cap — an attempt beyond the cap escalates to the maintainer;
//  3. ongoing interaction (preferring one that touched the failing target);
//  4. recent past interaction that produced the change;
//  5. otherwise dispatch a fresh session.
func (r DevLeadRouter) Route(event CIFailureEvent, interactions []LeadInteraction) (LeadRoutingDecision, error) {
	decision := LeadRoutingDecision{MaxAttempts: r.maxAttempts()}
	if err := event.Validate(); err != nil {
		decision.Reason = err.Error()
		return decision, err
	}
	decision.Repo = NormalizeRepo(event.Repo)
	key := event.FixSessionKey()
	decision.FixSessionKey = key
	now := r.now()
	window := r.recentWindow()

	decision.trailf("relayed %s (%s) for %s", event.kindOrDefault(), event.providerOrDefault(), decision.Repo)

	candidates := make([]LeadInteraction, 0, len(interactions))
	for _, interaction := range interactions {
		if NormalizeRepo(interaction.Repo) != decision.Repo {
			decision.trailf("ignored interaction %s: repo %q is not the failing repo", interaction.UUID, NormalizeRepo(interaction.Repo))
			continue
		}
		if !interaction.IsActive() && now.Sub(interaction.LastActivityAt.UTC()) > window {
			decision.trailf("ignored interaction %s: past and older than %s", interaction.UUID, window)
			continue
		}
		candidates = append(candidates, interaction)
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		if !candidates[a].LastActivityAt.Equal(candidates[b].LastActivityAt) {
			return candidates[a].LastActivityAt.After(candidates[b].LastActivityAt)
		}
		return candidates[a].UUID < candidates[b].UUID
	})
	if len(candidates) == 0 {
		decision.trailf("no live or recent interaction for %s", decision.Repo)
	} else {
		decision.trailf("candidates (most recent first): %s", joinInteractionUUIDs(candidates))
	}

	recorded := 0
	for _, candidate := range candidates {
		if attempts := candidate.RecordedAttempts(key); attempts > recorded {
			recorded = attempts
		}
	}
	attempt := event.Attempt
	if attempt <= 0 {
		attempt = recorded + 1
		decision.trailf("attempt not supplied; derived attempt %d from %d recorded fix attempt(s)", attempt, recorded)
	}
	decision.Attempt = attempt
	if recorded > 0 {
		decision.trailf("fix session %s already has %d recorded attempt(s)", key, recorded)
	}

	// 1. Dedupe: this exact attempt is already being handled.
	if recorded > 0 && attempt > 0 && attempt <= recorded {
		if owner, ok := pickFixOwner(candidates, key); ok {
			decision.Route = RouteDedupeExisting
			decision.TargetInteractionUUID = owner.UUID
			decision.TargetInteractionKind = owner.Kind
			decision.Reason = fmt.Sprintf("attempt %d of fix session %s is already being handled by %s; not starting a second fix session", attempt, key, owner.UUID)
			decision.trailf("dedupe: %s", decision.Reason)
			return decision, nil
		}
	}

	// 2. Fix-cycle cap.
	if attempt > decision.MaxAttempts {
		decision.Route = RouteEscalateMaintainer
		decision.Reason = fmt.Sprintf("fix-cycle cap exhausted for %s (%d/%d attempts); flagging maintainer intervention", key, decision.MaxAttempts, decision.MaxAttempts)
		decision.trailf("escalate: %s", decision.Reason)
		return decision, nil
	}

	// 3. Ongoing interaction.
	if owner, matchedTarget, ok := pickInteraction(candidates, event, LeadInteraction.IsActive); ok {
		decision.Route = RouteAppendOngoing
		decision.TargetInteractionUUID = owner.UUID
		decision.TargetInteractionKind = owner.Kind
		decision.Reason = fmt.Sprintf("ongoing interaction %s%s handles this repo; appending the failure as fix attempt %d/%d", owner.UUID, targetSuffix(matchedTarget), attempt, decision.MaxAttempts)
		decision.trailf("route ongoing: %s", decision.Reason)
		return decision, nil
	}

	// 4. Recent past interaction that produced the change.
	if owner, matchedTarget, ok := pickInteraction(candidates, event, func(i LeadInteraction) bool { return !i.IsActive() }); ok {
		decision.Route = RouteReactivatePast
		decision.TargetInteractionUUID = owner.UUID
		decision.TargetInteractionKind = owner.Kind
		decision.Reason = fmt.Sprintf("past interaction %s%s last touched this change; re-activating it for fix attempt %d/%d", owner.UUID, targetSuffix(matchedTarget), attempt, decision.MaxAttempts)
		decision.trailf("route past: %s", decision.Reason)
		return decision, nil
	}

	// 5. No owner: dispatch a fresh plan->implement session on the branch.
	decision.Route = RouteSpawnNew
	decision.Reason = fmt.Sprintf("no interaction owns fix session %s; dispatching a fresh plan->implement session (attempt %d/%d)", key, attempt, decision.MaxAttempts)
	decision.trailf("route new: %s", decision.Reason)
	return decision, nil
}

// pickFixOwner returns the most relevant interaction already attributed to the
// fix session, preferring a running one.
func pickFixOwner(candidates []LeadInteraction, fixSessionKey string) (LeadInteraction, bool) {
	var fallback LeadInteraction
	haveFallback := false
	for _, candidate := range candidates {
		if candidate.RecordedAttempts(fixSessionKey) <= 0 {
			continue
		}
		if candidate.IsActive() {
			return candidate, true
		}
		if !haveFallback {
			fallback, haveFallback = candidate, true
		}
	}
	return fallback, haveFallback
}

// pickInteraction returns the first candidate (most recent first) accepted by
// want, preferring one that actually touched the failing PR/branch/commit.
func pickInteraction(candidates []LeadInteraction, event CIFailureEvent, want func(LeadInteraction) bool) (LeadInteraction, bool, bool) {
	var fallback LeadInteraction
	haveFallback := false
	for _, candidate := range candidates {
		if !want(candidate) {
			continue
		}
		if candidate.MatchesTarget(event) {
			return candidate, true, true
		}
		if !haveFallback {
			fallback, haveFallback = candidate, true
		}
	}
	return fallback, false, false
}

func targetSuffix(matchedTarget bool) string {
	if matchedTarget {
		return " (last touched the failing branch/PR)"
	}
	return ""
}

func joinInteractionUUIDs(candidates []LeadInteraction) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		status := strings.TrimSpace(candidate.Status)
		if status == "" {
			status = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", candidate.UUID, status))
	}
	return strings.Join(parts, ", ")
}
