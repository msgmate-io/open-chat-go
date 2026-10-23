package msgmate

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ci#845 regression: one lane of one ticket collapses onto exactly one owner on
// every dispatcher path, while two lanes of the same ticket stay distinct owners.
// The test is pure: it touches the owner-key derivation and the guard's
// ownership table, no database, no queue, no clock.

func TestCoordinationOwnerGuardSameTicketLaneCollapsesToOneOwner(t *testing.T) {
	guard := NewCoordinationOwnerGuard()

	first := guard.Decide(CoordinationDispatchRequest{
		Ticket:          "ci#845",
		Lane:            "coordinator-code",
		Source:          CoordinationSourceWorkflowDispatch,
		InteractionUUID: "interaction-1",
	})
	if first.Outcome != CoordinationOutcomeAccepted || !first.StartsNewInteraction() {
		t.Fatalf("first dispatch must own the key, got outcome=%q reason=%q", first.Outcome, first.Reason)
	}
	if first.OwnerKey != CoordinationDedupeKey("ci#845", "coordinator-code") {
		t.Fatalf("owner key = %q, want the ticket+lane key", first.OwnerKey)
	}

	// A second dispatch of the same ticket+lane must not start a second
	// interaction: it re-attaches to the owner that is already live.
	second := guard.Decide(CoordinationDispatchRequest{
		Ticket: "ci#845",
		Lane:   "coordinator-code",
		Source: CoordinationSourceReconcile,
	})
	if second.Outcome != CoordinationOutcomeReattached {
		t.Fatalf("second dispatch outcome = %q, want %q", second.Outcome, CoordinationOutcomeReattached)
	}
	if second.StartsNewInteraction() {
		t.Fatalf("a re-attached dispatch must not start a new interaction")
	}
	if !second.Reattached() || second.Refused() {
		t.Fatalf("second dispatch flags: reattached=%v refused=%v", second.Reattached(), second.Refused())
	}
	if second.InteractionUUID != "interaction-1" {
		t.Fatalf("re-attached interaction = %q, want interaction-1", second.InteractionUUID)
	}
	if second.Reason != CoordinationReasonReattached {
		t.Fatalf("recorded reason = %q, want %q", second.Reason, CoordinationReasonReattached)
	}
	if second.OwnerKey != first.OwnerKey {
		t.Fatalf("owner keys differ: %q vs %q", second.OwnerKey, first.OwnerKey)
	}
	if !SameCoordinationOwner("ci#845", "coordinator-code", "ci#845", "coordinator-code") {
		t.Fatalf("SameCoordinationOwner must hold for one ticket+lane")
	}

	// Whitespace and case do not create a second owner either.
	messy := guard.Decide(CoordinationDispatchRequest{
		Ticket: " CI#845 ",
		Lane:   " COORDINATOR-CODE ",
		Source: CoordinationSourceSelfMessageSchedule,
	})
	if messy.Outcome != CoordinationOutcomeReattached || messy.OwnerKey != first.OwnerKey {
		t.Fatalf("messy duplicate: outcome=%q key=%q, want reattached on %q", messy.Outcome, messy.OwnerKey, first.OwnerKey)
	}

	if got := guard.Len(); got != 1 {
		t.Fatalf("live owners = %d, want 1 for one ticket+lane", got)
	}
}

func TestCoordinationOwnerGuardTwoLanesStayDistinct(t *testing.T) {
	guard := NewCoordinationOwnerGuard()

	code := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "coordinator-code", InteractionUUID: "interaction-code"})
	design := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "design", InteractionUUID: "interaction-design"})
	defaultLane := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", InteractionUUID: "interaction-default"})
	otherTicket := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#846", Lane: "coordinator-code", InteractionUUID: "interaction-846"})

	for name, decision := range map[string]CoordinationDispatchDecision{
		"coordinator-code": code,
		"design":           design,
		"default lane":     defaultLane,
		"other ticket":     otherTicket,
	} {
		if decision.Outcome != CoordinationOutcomeAccepted {
			t.Fatalf("%s: outcome = %q, want accepted", name, decision.Outcome)
		}
	}
	if code.OwnerKey == design.OwnerKey {
		t.Fatalf("two lanes of one ticket must not share an owner key: %q", code.OwnerKey)
	}
	if code.OwnerKey == defaultLane.OwnerKey {
		t.Fatalf("the default lane must be its own owner, got %q", defaultLane.OwnerKey)
	}
	if code.OwnerKey == otherTicket.OwnerKey {
		t.Fatalf("two tickets must not share an owner key: %q", code.OwnerKey)
	}
	if SameCoordinationOwner("ci#845", "coordinator-code", "ci#845", "design") {
		t.Fatalf("SameCoordinationOwner must reject two lanes of one ticket")
	}
	if got := guard.Len(); got != 4 {
		t.Fatalf("live owners = %d, want 4 (three lanes + another ticket)", got)
	}
}

func TestCoordinationOwnerGuardRefusesEmptyTicket(t *testing.T) {
	guard := NewCoordinationOwnerGuard()

	empty := guard.Decide(CoordinationDispatchRequest{Lane: "coordinator-code", Source: CoordinationSourceWorkflowDispatch})
	if empty.Outcome != CoordinationOutcomeRefusedEmptyTicket {
		t.Fatalf("outcome = %q, want %q", empty.Outcome, CoordinationOutcomeRefusedEmptyTicket)
	}
	if !empty.Refused() || empty.StartsNewInteraction() {
		t.Fatalf("an empty-ticket dispatch must be refused, got refused=%v starts=%v", empty.Refused(), empty.StartsNewInteraction())
	}
	if empty.OwnerKey != "" {
		t.Fatalf("owner key = %q, want empty: an unidentified dispatch must not invent an owner", empty.OwnerKey)
	}
	if empty.Reason != CoordinationReasonEmptyTicket {
		t.Fatalf("recorded reason = %q, want the empty-ticket reason", empty.Reason)
	}
	if got := guard.Len(); got != 0 {
		t.Fatalf("live owners = %d, want 0 after a refusal", got)
	}

	if _, ok := guard.Live("", "coordinator-code"); ok {
		t.Fatalf("an empty ticket must never be live")
	}
}

func TestCoordinationOwnerGuardReDispatchOfSameInteractionIsAccepted(t *testing.T) {
	guard := NewCoordinationOwnerGuard()
	req := CoordinationDispatchRequest{Ticket: "ci#845", Lane: "coordinator-code", InteractionUUID: "interaction-1"}

	if decision := guard.Decide(req); decision.Outcome != CoordinationOutcomeAccepted {
		t.Fatalf("first outcome = %q, want accepted", decision.Outcome)
	}
	again := guard.Decide(req)
	if again.Outcome != CoordinationOutcomeAccepted || !again.StartsNewInteraction() {
		t.Fatalf("re-dispatch of the owner's own interaction: outcome = %q, want accepted", again.Outcome)
	}
	if again.Reason != CoordinationReasonAcceptedSameOwner {
		t.Fatalf("recorded reason = %q, want the same-owner reason", again.Reason)
	}
	if got := guard.Len(); got != 1 {
		t.Fatalf("live owners = %d, want 1", got)
	}
}

func TestCoordinationOwnerGuardForceStartsDeliberateDuplicate(t *testing.T) {
	guard := NewCoordinationOwnerGuard()
	if decision := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "coordinator-code", InteractionUUID: "interaction-1"}); decision.Outcome != CoordinationOutcomeAccepted {
		t.Fatalf("setup outcome = %q, want accepted", decision.Outcome)
	}

	forced := guard.Decide(CoordinationDispatchRequest{
		Ticket:          "ci#845",
		Lane:            "coordinator-code",
		InteractionUUID: "interaction-2",
		Force:           true,
	})
	if forced.Outcome != CoordinationOutcomeAccepted || !forced.StartsNewInteraction() {
		t.Fatalf("forced outcome = %q, want accepted", forced.Outcome)
	}
	if forced.Reason != CoordinationReasonAcceptedForced || !forced.Forced {
		t.Fatalf("forced decision must record that it overrode a live owner: reason=%q forced=%v", forced.Reason, forced.Forced)
	}
	live, ok := guard.Live("ci#845", "coordinator-code")
	if !ok || live.InteractionUUID != "interaction-2" {
		t.Fatalf("live owner after force = %+v, want interaction-2", live)
	}
}

func TestCoordinationOwnerGuardRefusesOwnerWithoutInteraction(t *testing.T) {
	guard := NewCoordinationOwnerGuard()
	if decision := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "coordinator-code"}); decision.Outcome != CoordinationOutcomeAccepted {
		t.Fatalf("setup outcome = %q, want accepted", decision.Outcome)
	}

	live := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "coordinator-code", InteractionUUID: "interaction-2"})
	if live.Outcome != CoordinationOutcomeRefusedOwnerLive {
		t.Fatalf("outcome = %q, want %q (nothing to re-attach to)", live.Outcome, CoordinationOutcomeRefusedOwnerLive)
	}
	if !live.Refused() || live.StartsNewInteraction() {
		t.Fatalf("a wedged owner must refuse the dispatch, got refused=%v starts=%v", live.Refused(), live.StartsNewInteraction())
	}
	if live.Reason != CoordinationReasonOwnerLive {
		t.Fatalf("recorded reason = %q, want the live-owner reason", live.Reason)
	}
}

func TestCoordinationOwnerGuardReconcileReleasesDeadOwners(t *testing.T) {
	guard := NewCoordinationOwnerGuard()
	guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "coordinator-code", InteractionUUID: "interaction-live"})
	guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "design", InteractionUUID: "interaction-dead"})
	guard.Decide(CoordinationDispatchRequest{Ticket: "ci#846", InteractionUUID: "interaction-wedged"})
	guard.Decide(CoordinationDispatchRequest{Ticket: "ci#847", Lane: "qa"}) // owner without an interaction

	released := guard.Reconcile([]string{"interaction-live"})
	got := make([]string, 0, len(released))
	for _, owner := range released {
		got = append(got, owner.OwnerKey)
	}
	want := []string{CoordinationDedupeKey("ci#845", "design"), CoordinationDedupeKey("ci#846", ""), CoordinationDedupeKey("ci#847", "qa")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("released owner keys = %v, want %v (sorted)", got, want)
	}
	if got := guard.Len(); got != 1 {
		t.Fatalf("live owners after reconcile = %d, want 1", got)
	}

	// The lane that was released can be dispatched again, and it is accepted.
	again := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "design", InteractionUUID: "interaction-dead-2"})
	if again.Outcome != CoordinationOutcomeAccepted {
		t.Fatalf("released lane outcome = %q, want accepted", again.Outcome)
	}
	// The live owner keeps its key: its dispatch re-attaches instead.
	if decision := guard.Decide(CoordinationDispatchRequest{Ticket: "ci#845", Lane: "coordinator-code"}); decision.Outcome != CoordinationOutcomeReattached {
		t.Fatalf("live lane outcome = %q, want reattached", decision.Outcome)
	}
}

// The core acceptance of ci#845: the three live dispatcher paths share one owner
// table, so the same ticket+lane dispatched through any of them collapses onto
// one owner.
func TestCoordinationOwnerGuardDispatcherPathsShareOneOwnerKey(t *testing.T) {
	guard := NewCoordinationOwnerGuard()

	workflow := guard.Decide(CoordinationDispatchRequest{
		Ticket:          "ci#845",
		Lane:            "coordinator-code",
		Source:          CoordinationSourceWorkflowDispatch,
		InteractionUUID: "interaction-1",
	})
	if workflow.Outcome != CoordinationOutcomeAccepted {
		t.Fatalf("workflow dispatch outcome = %q, want accepted", workflow.Outcome)
	}

	scheduled := guard.Decide(CoordinationDispatchRequest{
		Ticket: "ci#845",
		Lane:   "coordinator-code",
		Source: CoordinationSourceSelfMessageSchedule,
	})
	reconciled := guard.Decide(CoordinationDispatchRequest{
		Ticket: "ci#845",
		Lane:   "coordinator-code",
		Source: CoordinationSourceReconcile,
	})
	for name, decision := range map[string]CoordinationDispatchDecision{"self-message schedule": scheduled, "reconcile": reconciled} {
		if decision.Outcome != CoordinationOutcomeReattached {
			t.Fatalf("%s outcome = %q, want reattached", name, decision.Outcome)
		}
		if decision.OwnerKey != workflow.OwnerKey {
			t.Fatalf("%s owner key = %q, want %q", name, decision.OwnerKey, workflow.OwnerKey)
		}
	}
	if got := guard.Len(); got != 1 {
		t.Fatalf("live owners = %d, want exactly one across all dispatch paths", got)
	}
}

func TestCoordinationOwnerGuardConcurrentDispatchesCollapseToOneOwner(t *testing.T) {
	guard := NewCoordinationOwnerGuard()

	const dispatchers = 32
	var wg sync.WaitGroup
	decisions := make([]CoordinationDispatchDecision, dispatchers)
	for i := 0; i < dispatchers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			decisions[i] = guard.Decide(CoordinationDispatchRequest{
				Ticket:          "ci#845",
				Lane:            "coordinator-code",
				InteractionUUID: fmt.Sprintf("interaction-%d", i),
			})
		}(i)
	}
	wg.Wait()

	accepted := 0
	for _, decision := range decisions {
		switch decision.Outcome {
		case CoordinationOutcomeAccepted:
			accepted++
		case CoordinationOutcomeReattached:
		default:
			t.Fatalf("unexpected outcome %q", decision.Outcome)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted dispatch %d, want exactly 1", accepted)
	}
	if got := guard.Len(); got != 1 {
		t.Fatalf("live owners = %d, want 1", got)
	}
}

func TestCoordinationTicketAndLaneFromTags(t *testing.T) {
	tests := []struct {
		name       string
		ticket     string
		lane       string
		tags       []string
		wantTicket string
		wantLane   string
		wantKeyed  bool
	}{
		{
			name:       "tags name the ticket and the lane",
			tags:       []string{"repo:open-chat-go", "issue:845", "lane:coordinator-code"},
			wantTicket: "845",
			wantLane:   "coordinator-code",
			wantKeyed:  true,
		},
		{
			name:       "explicit fields win over tags",
			ticket:     "ci#845",
			lane:       "coordinator-code",
			tags:       []string{"issue:999", "lane:other"},
			wantTicket: "ci#845",
			wantLane:   "coordinator-code",
			wantKeyed:  true,
		},
		{
			name:      "an untagged dispatch is not owner-keyed",
			tags:      []string{"repo:open-chat-go"},
			wantKeyed: false,
		},
		{
			name:      "a lane without a ticket is owner-keyed but has no ticket",
			tags:      []string{"lane:coordinator-code"},
			wantLane:  "coordinator-code",
			wantKeyed: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticket, lane, keyed := CoordinationTicketAndLane(tc.ticket, tc.lane, tc.tags)
			if keyed != tc.wantKeyed {
				t.Fatalf("owner-keyed = %v, want %v", keyed, tc.wantKeyed)
			}
			if !keyed {
				if ticket != "" || lane != "" {
					t.Fatalf("non owner-keyed dispatch returned (%q,%q), want empty", ticket, lane)
				}
				return
			}
			if ticket != tc.wantTicket || lane != tc.wantLane {
				t.Fatalf("(ticket, lane) = (%q,%q), want (%q,%q)", ticket, lane, tc.wantTicket, tc.wantLane)
			}
		})
	}
}

// A dispatch that names a lane but no ticket is owner-keyed and then refused: the
// guard must not invent an owner from a lane alone.
func TestCoordinationOwnerGuardRefusesLaneOnlyDispatch(t *testing.T) {
	guard := NewCoordinationOwnerGuard()
	ticket, lane, keyed := CoordinationTicketAndLane("", "", []string{"lane:coordinator-code"})
	if !keyed {
		t.Fatalf("a lane-only dispatch is owner-keyed")
	}
	decision := guard.Decide(CoordinationDispatchRequest{Ticket: ticket, Lane: lane, Source: CoordinationSourceWorkflowDispatch})
	if decision.Outcome != CoordinationOutcomeRefusedEmptyTicket || !decision.Refused() {
		t.Fatalf("lane-only dispatch outcome = %q, want %q", decision.Outcome, CoordinationOutcomeRefusedEmptyTicket)
	}
}
