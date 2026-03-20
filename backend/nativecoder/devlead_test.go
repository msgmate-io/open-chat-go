package nativecoder

import (
	"strings"
	"testing"
	"time"
)

func testCIFailureEvent() CIFailureEvent {
	return CIFailureEvent{
		SchemaVersion: 1,
		Kind:          CIFailureEventKind,
		Provider:      "github",
		Repo:          "Msgmate-IO/Open-Chat-Go",
		PRNumber:      608,
		Branch:        "feat/devlead-relay",
		Sha:           "ABCDEF1234567890",
		RunURL:        "https://example.test/run/1",
		FailingJobs:   []string{"backend-tests", "gofmt"},
		FailureLogs:   "--- FAIL: TestRoute (0.01s)",
		Severity:      "high",
	}
}

func TestParseCIFailureEvent(t *testing.T) {
	raw := []byte(`{"kind":"ci_failure","schema_version":1,"provider":"github","repo":"Msgmate-IO/Open-Chat-Go.git","pr_number":608,"branch":"refs/heads/Feat/X","failing_jobs":["build"]}`)
	event, err := ParseCIFailureEvent(raw)
	if err != nil {
		t.Fatalf("ParseCIFailureEvent: %v", err)
	}
	if event.Repo != "Msgmate-IO/Open-Chat-Go.git" {
		t.Fatalf("repo should be preserved verbatim on the event, got %q", event.Repo)
	}
	if got := event.FixSessionKey(); got != "msgmate-io/open-chat-go#pr-608" {
		t.Fatalf("FixSessionKey = %q", got)
	}
	if got := NormalizeBranch(event.Branch); got != "feat/x" {
		t.Fatalf("NormalizeBranch = %q", got)
	}
}

func TestParseCIFailureEventRejectsBadPayloads(t *testing.T) {
	cases := map[string]string{
		"empty":         ``,
		"not json":      `{`,
		"missing repo":  `{"kind":"ci_failure"}`,
		"unknown kind":  `{"kind":"something_else","repo":"a/b"}`,
		"future schema": `{"repo":"a/b","schema_version":99}`,
		"negative pr":   `{"repo":"a/b","pr_number":-1}`,
	}
	for name, raw := range cases {
		if _, err := ParseCIFailureEvent([]byte(raw)); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestFixSessionKeyPrefersPRThenBranchThenCommit(t *testing.T) {
	event := testCIFailureEvent()
	if got, want := event.FixSessionKey(), "msgmate-io/open-chat-go#pr-608"; got != want {
		t.Fatalf("pr key = %q, want %q", got, want)
	}
	event.PRNumber = 0
	if got, want := event.FixSessionKey(), "msgmate-io/open-chat-go#branch-feat/devlead-relay"; got != want {
		t.Fatalf("branch key = %q, want %q", got, want)
	}
	event.Branch = ""
	if got, want := event.FixSessionKey(), "msgmate-io/open-chat-go#sha-abcdef1234567890"; got != want {
		t.Fatalf("sha key = %q, want %q", got, want)
	}
}

func TestRouteAppendsToOngoingInteraction(t *testing.T) {
	event := testCIFailureEvent()
	now := time.Now().UTC()
	interactions := []LeadInteraction{
		{UUID: "old-other", Status: LeadInteractionCompleted, Repo: "msgmate-io/open-chat-go", Branch: "chore/other", LastActivityAt: now.Add(-time.Hour)},
		{UUID: "ongoing", Kind: "msgmate-coder", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go", Branch: "feat/devlead-relay", PRNumber: 608, LastActivityAt: now.Add(-3 * time.Minute)},
	}
	decision, err := DevLeadRouter{Now: func() time.Time { return now }}.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Route != RouteAppendOngoing {
		t.Fatalf("route = %q, want %q", decision.Route, RouteAppendOngoing)
	}
	if decision.TargetInteractionUUID != "ongoing" {
		t.Fatalf("target = %q, want ongoing", decision.TargetInteractionUUID)
	}
	if decision.Attempt != 1 || decision.MaxAttempts != DefaultFixCycleMaxAttempts {
		t.Fatalf("attempt = %d/%d", decision.Attempt, decision.MaxAttempts)
	}
	if !decision.Dispatch() {
		t.Fatalf("append must dispatch")
	}
	prompt := decision.Prompt(event)
	for _, want := range []string{CIFailureFixMarker, "pull request: #608", "branch:", "backend-tests, gofmt", NeverPollCINote, "route: `append_ongoing`"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if len(decision.Trail) == 0 {
		t.Fatalf("expected a visible routing trail")
	}
}

func TestRouteReactivatesPastInteractionOwningTheBranch(t *testing.T) {
	event := testCIFailureEvent()
	now := time.Now().UTC()
	interactions := []LeadInteraction{
		{UUID: "unrelated-active", Kind: "msgmate-coder", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go", Branch: "chore/consolidate-2026", LastActivityAt: now.Add(-time.Minute)},
		{UUID: "producer", Kind: "msgmate-coder", Status: LeadInteractionCompleted, Repo: "msgmate-io/open-chat-go", Branch: "feat/devlead-relay", PRNumber: 608, LastActivityAt: now.Add(-2 * time.Hour)},
	}
	decision, err := DevLeadRouter{Now: func() time.Time { return now }}.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Route != RouteReactivatePast {
		t.Fatalf("route = %q, want %q", decision.Route, RouteReactivatePast)
	}
	if decision.TargetInteractionUUID != "producer" {
		t.Fatalf("target = %q, want producer (the branch/PR owner)", decision.TargetInteractionUUID)
	}
	if !strings.Contains(decision.Reason, "re-activating") {
		t.Fatalf("reason = %q", decision.Reason)
	}
	// The prompt names the target so the trail is visible in the interaction.
	if prompt := decision.Prompt(event); !strings.Contains(prompt, "target interaction: `producer`") {
		t.Fatalf("prompt missing target: %s", prompt)
	}
}

func TestRouteSpawnsNewSessionWithoutOwner(t *testing.T) {
	event := testCIFailureEvent()
	now := time.Now().UTC()
	interactions := []LeadInteraction{
		{UUID: "other-repo", Status: LeadInteractionActive, Repo: "msgmate-io/other", LastActivityAt: now},
	}
	decision, err := DevLeadRouter{Now: func() time.Time { return now }}.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Route != RouteSpawnNew {
		t.Fatalf("route = %q, want %q", decision.Route, RouteSpawnNew)
	}
	if decision.TargetInteractionUUID != "" {
		t.Fatalf("spawn-new must not target an existing interaction, got %q", decision.TargetInteractionUUID)
	}
	if !decision.Dispatch() {
		t.Fatalf("spawn-new must dispatch")
	}
	// A stale past interaction outside the recent window is not a candidate.
	stale := []LeadInteraction{{
		UUID: "stale", Status: LeadInteractionCompleted, Repo: "msgmate-io/open-chat-go",
		Branch: "feat/devlead-relay", LastActivityAt: now.Add(-72 * time.Hour),
	}}
	decision, err = DevLeadRouter{Now: func() time.Time { return now }}.Route(event, stale)
	if err != nil {
		t.Fatalf("Route(stale): %v", err)
	}
	if decision.Route != RouteSpawnNew {
		t.Fatalf("stale route = %q, want %q", decision.Route, RouteSpawnNew)
	}
}

func TestRouteDedupesAlreadyOpenFixSession(t *testing.T) {
	event := testCIFailureEvent()
	event.Attempt = 1
	now := time.Now().UTC()
	interactions := []LeadInteraction{{
		UUID: "fixer", Kind: "msgmate-coder", Status: LeadInteractionActive,
		Repo: "msgmate-io/open-chat-go", Branch: "feat/devlead-relay", PRNumber: 608,
		LastActivityAt: now,
		FixAttempts:    map[string]int{"msgmate-io/open-chat-go#pr-608": 1},
	}}
	decision, err := DevLeadRouter{Now: func() time.Time { return now }}.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Route != RouteDedupeExisting {
		t.Fatalf("route = %q, want %q", decision.Route, RouteDedupeExisting)
	}
	if decision.Dispatch() {
		t.Fatalf("dedupe must not dispatch a new fix attempt")
	}
	if decision.TargetInteractionUUID != "fixer" {
		t.Fatalf("dedupe target = %q", decision.TargetInteractionUUID)
	}
	if _, err := ParseCIFailureEvent([]byte(decision.Prompt(event))); err == nil {
		t.Fatalf("sanity: prompt is not a JSON payload")
	}
}

func TestRouteEscalatesWhenFixCycleCapExhausted(t *testing.T) {
	now := time.Now().UTC()
	interactions := []LeadInteraction{{
		UUID: "fixer", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go",
		PRNumber: 608, LastActivityAt: now,
		FixAttempts: map[string]int{"msgmate-io/open-chat-go#pr-608": DefaultFixCycleMaxAttempts},
	}}
	event := testCIFailureEvent()
	event.Attempt = DefaultFixCycleMaxAttempts + 1
	decision, err := DevLeadRouter{Now: func() time.Time { return now }}.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Route != RouteEscalateMaintainer {
		t.Fatalf("route = %q, want %q", decision.Route, RouteEscalateMaintainer)
	}
	if decision.Dispatch() {
		t.Fatalf("escalation must not dispatch")
	}
	if !strings.Contains(decision.Reason, "maintainer") {
		t.Fatalf("reason = %q", decision.Reason)
	}
	// A custom cap is honoured (no recorded attempts, so this is a fresh
	// attempt past a cap of 1).
	fresh := []LeadInteraction{{
		UUID: "fixer", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go",
		PRNumber: 608, LastActivityAt: now,
	}}
	event.Attempt = 2
	decision, err = DevLeadRouter{MaxAttempts: 1, Now: func() time.Time { return now }}.Route(event, fresh)
	if err != nil {
		t.Fatalf("Route(cap 1): %v", err)
	}
	if decision.Route != RouteEscalateMaintainer || decision.MaxAttempts != 1 {
		t.Fatalf("custom cap route = %q max = %d", decision.Route, decision.MaxAttempts)
	}
}

func TestRouteDerivesAttemptFromRecordedAttempts(t *testing.T) {
	now := time.Now().UTC()
	event := testCIFailureEvent()
	interactions := []LeadInteraction{{
		UUID: "fixer", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go",
		PRNumber: 608, LastActivityAt: now,
		FixAttempts: map[string]int{"msgmate-io/open-chat-go#pr-608": 1},
	}}
	decision, err := DevLeadRouter{Now: func() time.Time { return now }}.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2 (derived from 1 recorded)", decision.Attempt)
	}
	if decision.Route != RouteAppendOngoing {
		t.Fatalf("route = %q, want %q", decision.Route, RouteAppendOngoing)
	}
	if !strings.Contains(strings.Join(decision.Trail, "\n"), "derived attempt 2") {
		t.Fatalf("trail = %v", decision.Trail)
	}
}

func TestRouteStartsOnlyOneFixAttemptPerRelayedFailure(t *testing.T) {
	// Two consecutive relays of the same failure must not start two attempts.
	now := time.Now().UTC()
	event := testCIFailureEvent()
	event.Attempt = 1
	interactions := []LeadInteraction{{
		UUID: "fixer", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go",
		PRNumber: 608, LastActivityAt: now,
	}}
	router := DevLeadRouter{Now: func() time.Time { return now }}
	first, err := router.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !first.Dispatch() {
		t.Fatalf("first relay should dispatch, got %q", first.Route)
	}
	// The lead records the attempt on the interaction, then the same event is
	// relayed again (eg a workflow re-run).
	interactions[0].FixAttempts = map[string]int{first.FixSessionKey: first.Attempt}
	second, err := router.Route(event, interactions)
	if err != nil {
		t.Fatalf("Route(second): %v", err)
	}
	if second.Route != RouteDedupeExisting || second.Dispatch() {
		t.Fatalf("second relay = %q dispatch=%v, want a dedupe with no dispatch", second.Route, second.Dispatch())
	}
}

func TestRouteRejectsInvalidEvent(t *testing.T) {
	decision, err := DevLeadRouter{}.Route(CIFailureEvent{Kind: CIFailureEventKind}, nil)
	if err == nil {
		t.Fatalf("expected an error for a repo-less event")
	}
	if decision.Dispatch() {
		t.Fatalf("invalid event must not dispatch")
	}
}

func TestCandidateInteractionsFiltersAndOrders(t *testing.T) {
	now := time.Now().UTC()
	router := DevLeadRouter{Now: func() time.Time { return now }}
	got := router.CandidateInteractions("Msgmate-IO/Open-Chat-Go.git", []LeadInteraction{
		{UUID: "stale", Status: LeadInteractionCompleted, Repo: "msgmate-io/open-chat-go", LastActivityAt: now.Add(-72 * time.Hour)},
		{UUID: "other-repo", Status: LeadInteractionActive, Repo: "msgmate-io/other", LastActivityAt: now},
		{UUID: "older", Status: LeadInteractionCompleted, Repo: "msgmate-io/open-chat-go", LastActivityAt: now.Add(-2 * time.Hour)},
		{UUID: "newer", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go", LastActivityAt: now.Add(-time.Minute)},
	})
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want the two fresh same-repo interactions", got)
	}
	if got[0].UUID != "newer" || got[1].UUID != "older" {
		t.Fatalf("order = %s, %s; want newer then older", got[0].UUID, got[1].UUID)
	}
	// Deterministic tiebreak on equal last-activity.
	tie := router.CandidateInteractions("msgmate-io/open-chat-go", []LeadInteraction{
		{UUID: "b", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go", LastActivityAt: now},
		{UUID: "a", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go", LastActivityAt: now},
	})
	if tie[0].UUID != "a" || tie[1].UUID != "b" {
		t.Fatalf("tie order = %s, %s", tie[0].UUID, tie[1].UUID)
	}
}

func TestResolveLeadInteraction(t *testing.T) {
	now := time.Now().UTC()
	router := DevLeadRouter{Now: func() time.Time { return now }}
	interactions := []LeadInteraction{
		{UUID: "spawned-coder", Kind: "msgmate-coder", Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go", LastActivityAt: now},
		{UUID: "the-lead", Kind: DevLeadInteractionKind, Status: LeadInteractionActive, Repo: "msgmate-io/open-chat-go", LastActivityAt: now.Add(-time.Hour)},
		{UUID: "past-lead", Kind: DevLeadInteractionKind, Status: LeadInteractionCompleted, Repo: "msgmate-io/open-chat-go", LastActivityAt: now.Add(-time.Hour)},
	}
	got, ok := router.ResolveLeadInteraction("Msgmate-IO/Open-Chat-Go", interactions)
	if !ok || got.UUID != "the-lead" {
		t.Fatalf("resolve = %q/%v, want the-lead", got.UUID, ok)
	}
	// No explicit lead: the most recent running interaction.
	got, ok = router.ResolveLeadInteraction("msgmate-io/open-chat-go", interactions[:1])
	if !ok || got.UUID != "spawned-coder" {
		t.Fatalf("fallback = %q/%v", got.UUID, ok)
	}
	// Only a recent past interaction: reseat on it rather than spawn blindly.
	got, ok = router.ResolveLeadInteraction("msgmate-io/open-chat-go", []LeadInteraction{interactions[2]})
	if !ok || got.UUID != "past-lead" {
		t.Fatalf("past = %q/%v", got.UUID, ok)
	}
	if _, ok := router.ResolveLeadInteraction("msgmate-io/open-chat-go", nil); ok {
		t.Fatalf("no interactions must not resolve")
	}
}

// TestNormalizeRepoDevLead covers the repo normalization as the development
// lead depends on it; the canonical implementation lives in tags.go and is
// covered by TestNormalizeRepo in tags_test.go.
func TestNormalizeRepoDevLead(t *testing.T) {
	if got := NormalizeRepo("  Msgmate-IO/Open-Chat-Go.git "); got != "msgmate-io/open-chat-go" {
		t.Fatalf("NormalizeRepo = %q", got)
	}
}
