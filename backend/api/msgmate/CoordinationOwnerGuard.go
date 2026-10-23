package msgmate

import (
	"sort"
	"strings"
	"sync"
)

// ci#845 (leaf of the ci#747 umbrella, RC7/QW4): CoordinationDedupeKey and
// SameCoordinationOwner derived the owner key of one lane of one ticket, but no
// live dispatcher path called them, so the server could still open a second
// interaction for a unit of work that already had an owner (two ci#744
// interactions, duplicate ci#824 owners).
//
// This file is the missing enforcement point: every dispatch path - the
// workflow-driven dispatch (`POST /api/v1/agents/dispatch`), the self-message
// schedule and the reconcile step - hands its (ticket, lane) to a guard that
// derives the *same* owner key for the same unit of work and either
//   - accepts the dispatch and owns the key, or
//   - re-attaches it to the live owner (no second interaction), or
//   - refuses it, recording the reason.
//
// An empty ticket is always refused: an unidentified dispatch must not invent an
// owner, and an owner it cannot name is an owner it cannot later release. A
// refusal never starts work, so the failure mode is "work does not start twice",
// not "work does not start".
//
// The decision is recorded (`Reason`, `Outcome`) so a watcher - and the
// dispatcher's own JSON response - can report why a dispatch did not start a new
// interaction instead of silently doing nothing.
//
// The guard is deliberately transport-agnostic and clock-free: it holds the
// in-process ownership table, while the durable trace of an owner is the owner
// key itself (the dispatchers also tag the interaction with the key, so a
// restarted server re-discovers a live owner from the database rather than from
// this table).

// CoordinationDispatchSource names the dispatcher path that asked for an owner
// key. It is recorded on the decision so a refusal can name the path that was
// refused.
type CoordinationDispatchSource string

const (
	// CoordinationSourceWorkflowDispatch is the workflow-equivalent agent
	// dispatch verb (`POST /api/v1/agents/dispatch`).
	CoordinationSourceWorkflowDispatch CoordinationDispatchSource = "workflow_dispatch"

	// CoordinationSourceSelfMessageSchedule is the self-message scheduler's
	// dispatch of a scheduled turn.
	CoordinationSourceSelfMessageSchedule CoordinationDispatchSource = "self_message_schedule"

	// CoordinationSourceReconcile is the reconcile step that walks in-flight
	// work and re-dispatches what is missing.
	CoordinationSourceReconcile CoordinationDispatchSource = "reconcile"
)

// CoordinationDispatchOutcome is the recorded verdict of one dispatch attempt.
type CoordinationDispatchOutcome string

const (
	// CoordinationOutcomeAccepted means the dispatch owns the key now.
	CoordinationOutcomeAccepted CoordinationDispatchOutcome = "accepted"

	// CoordinationOutcomeReattached means the key was already owned by a live
	// interaction; the caller must steer that one instead of starting a second.
	CoordinationOutcomeReattached CoordinationDispatchOutcome = "reattached"

	// CoordinationOutcomeRefusedEmptyTicket means the dispatch carried no ticket,
	// so no owner key could be derived.
	CoordinationOutcomeRefusedEmptyTicket CoordinationDispatchOutcome = "refused_empty_ticket"

	// CoordinationOutcomeRefusedOwnerLive means the key is owned by an owner the
	// caller may not re-attach to (a wedged owner without an interaction).
	CoordinationOutcomeRefusedOwnerLive CoordinationDispatchOutcome = "refused_owner_live"
)

// Recorded reasons. They are stable strings (not fmt-verbs) so tests and agents
// can match on them.
const (
	// CoordinationReasonAccepted is recorded when a dispatch takes a free key.
	CoordinationReasonAccepted = "owner key was free: this dispatch owns the ticket+lane (ci#845)"

	// CoordinationReasonAcceptedSameOwner is recorded when the same interaction
	// re-registers its own key (an idempotent re-dispatch of the owner itself).
	CoordinationReasonAcceptedSameOwner = "same interaction already owns this ticket+lane (ci#845)"

	// CoordinationReasonAcceptedForced is recorded when `force` overrode a live
	// owner and a deliberate duplicate was started.
	CoordinationReasonAcceptedForced = "forced dispatch started a deliberate duplicate of a live owner (ci#845)"

	// CoordinationReasonEmptyTicket is recorded when the dispatch named no ticket.
	CoordinationReasonEmptyTicket = "refused: no ticket, so no ticket+lane owner key exists; an unidentified dispatch must not invent an owner (ci#845)"

	// CoordinationReasonReattached is recorded when the dispatch was routed to the
	// interaction that already owns the key.
	CoordinationReasonReattached = "re-attached to the live owner of this ticket+lane instead of opening a second interaction (ci#845)"

	// CoordinationReasonOwnerLive is recorded when the key is owned but the owner
	// has no interaction the caller could re-attach to.
	CoordinationReasonOwnerLive = "refused: this ticket+lane already has a live owner (ci#845)"

	// CoordinationReasonReleased is recorded for an owner that is no longer live
	// and whose key the reconcile step retired.
	CoordinationReasonReleased = "owner released: it is no longer live, so the ticket+lane is free again (ci#845)"
)

// CoordinationDispatchRequest is one dispatch attempt on one dispatcher path.
type CoordinationDispatchRequest struct {
	// Ticket identifies the unit of work (eg `ci#845`). An empty ticket is
	// refused.
	Ticket string

	// Lane names the parallel unit inside the ticket. An empty lane means "the
	// default lane", never "any lane", so a ticket's default lane and one named
	// lane are distinct owners.
	Lane string

	// Source names the dispatcher path, for the recorded reason.
	Source CoordinationDispatchSource

	// InteractionUUID is the interaction this dispatch is (or would be) attached
	// to. A re-dispatch of the owner's own interaction is accepted, not refused.
	InteractionUUID string

	// Force starts a deliberate duplicate even when the owner key is live.
	Force bool
}

// CoordinationOwnerRegistration is one live owner of one ticket+lane.
type CoordinationOwnerRegistration struct {
	OwnerKey        string                     `json:"owner_key"`
	Ticket          string                     `json:"ticket"`
	Lane            string                     `json:"lane"`
	InteractionUUID string                     `json:"interaction_uuid,omitempty"`
	Source          CoordinationDispatchSource `json:"source,omitempty"`
}

// CoordinationDispatchDecision is the recorded verdict of one dispatch attempt.
type CoordinationDispatchDecision struct {
	Outcome         CoordinationDispatchOutcome    `json:"outcome"`
	Reason          string                         `json:"reason"`
	OwnerKey        string                         `json:"owner_key,omitempty"`
	Ticket          string                         `json:"ticket,omitempty"`
	Lane            string                         `json:"lane,omitempty"`
	Source          CoordinationDispatchSource     `json:"source,omitempty"`
	InteractionUUID string                         `json:"interaction_uuid,omitempty"`
	Owner           *CoordinationOwnerRegistration `json:"owner,omitempty"`
	Forced          bool                           `json:"forced,omitempty"`
}

// StartsNewInteraction reports whether the decision lets the caller create an
// interaction. Only `accepted` does: a re-attached dispatch must steer the
// existing owner and a refusal must not start work at all.
func (d CoordinationDispatchDecision) StartsNewInteraction() bool {
	return d.Outcome == CoordinationOutcomeAccepted
}

// Refused reports whether the dispatch was refused outright (nothing starts).
func (d CoordinationDispatchDecision) Refused() bool {
	return d.Outcome == CoordinationOutcomeRefusedEmptyTicket || d.Outcome == CoordinationOutcomeRefusedOwnerLive
}

// Reattached reports whether the dispatch was routed to the live owner.
func (d CoordinationDispatchDecision) Reattached() bool {
	return d.Outcome == CoordinationOutcomeReattached
}

// CoordinationOwnerGuard enforces one owner per ticket+lane across the live
// dispatcher paths.
type CoordinationOwnerGuard struct {
	mu     sync.Mutex
	owners map[string]CoordinationOwnerRegistration
}

// NewCoordinationOwnerGuard returns an empty guard.
func NewCoordinationOwnerGuard() *CoordinationOwnerGuard {
	return &CoordinationOwnerGuard{owners: map[string]CoordinationOwnerRegistration{}}
}

var defaultCoordinationOwnerGuard = NewCoordinationOwnerGuard()

// DefaultCoordinationOwnerGuard is the process-wide guard the live dispatcher
// paths share: one server, one ownership table.
func DefaultCoordinationOwnerGuard() *CoordinationOwnerGuard {
	return defaultCoordinationOwnerGuard
}

// Decide derives the owner key of the request and either takes it, re-attaches
// the request to its live owner, or refuses it - recording the reason on the
// returned decision. An accepted decision registers the caller as the owner, so a
// second dispatch of the same ticket+lane can never also be accepted.
func (g *CoordinationOwnerGuard) Decide(req CoordinationDispatchRequest) CoordinationDispatchDecision {
	if g == nil {
		// A nil guard must not silently allow duplicates: treat it as "no
		// coordination state", which is the free-key case.
		g = NewCoordinationOwnerGuard()
	}
	ticket := strings.TrimSpace(req.Ticket)
	lane := strings.TrimSpace(req.Lane)
	key := CoordinationDedupeKey(ticket, lane)
	decision := CoordinationDispatchDecision{
		OwnerKey: key,
		Ticket:   ticket,
		Lane:     lane,
		Source:   req.Source,
		Forced:   req.Force,
	}
	if key == "" {
		decision.Outcome = CoordinationOutcomeRefusedEmptyTicket
		decision.Reason = CoordinationReasonEmptyTicket
		return decision
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owners == nil {
		g.owners = map[string]CoordinationOwnerRegistration{}
	}
	if live, ok := g.owners[key]; ok {
		owner := live
		decision.Owner = &owner
		sameInteraction := req.InteractionUUID != "" && live.InteractionUUID == req.InteractionUUID
		if sameInteraction {
			decision.Outcome = CoordinationOutcomeAccepted
			decision.Reason = CoordinationReasonAcceptedSameOwner
			decision.InteractionUUID = live.InteractionUUID
			return decision
		}
		if req.Force {
			g.owners[key] = registrationFromRequest(req, key)
			decision.Outcome = CoordinationOutcomeAccepted
			decision.Reason = CoordinationReasonAcceptedForced
			decision.InteractionUUID = req.InteractionUUID
			return decision
		}
		if live.InteractionUUID != "" {
			// Re-attach: the caller steers the existing owner and no second
			// interaction is created.
			decision.Outcome = CoordinationOutcomeReattached
			decision.Reason = CoordinationReasonReattached
			decision.InteractionUUID = live.InteractionUUID
			return decision
		}
		// Owned, but by an owner with nothing to re-attach to.
		decision.Outcome = CoordinationOutcomeRefusedOwnerLive
		decision.Reason = CoordinationReasonOwnerLive
		return decision
	}

	g.owners[key] = registrationFromRequest(req, key)
	decision.Outcome = CoordinationOutcomeAccepted
	decision.Reason = CoordinationReasonAccepted
	decision.InteractionUUID = req.InteractionUUID
	return decision
}

// Register records (or fills in the interaction of) the owner of the request's
// ticket+lane. A dispatcher that only learns the interaction UUID after creating
// it calls this once it has one; a re-registration of the same interaction is a
// no-op. It returns false when the key is owned by a different live interaction,
// so a late registration cannot steal an owner.
func (g *CoordinationOwnerGuard) Register(req CoordinationDispatchRequest, interactionUUID string) bool {
	if g == nil {
		return false
	}
	key := CoordinationDedupeKey(req.Ticket, req.Lane)
	if key == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owners == nil {
		g.owners = map[string]CoordinationOwnerRegistration{}
	}
	live, ok := g.owners[key]
	if ok && live.InteractionUUID != "" && interactionUUID != "" && live.InteractionUUID != interactionUUID {
		return false
	}
	registration := registrationFromRequest(req, key)
	if interactionUUID != "" {
		registration.InteractionUUID = interactionUUID
	} else if ok {
		registration.InteractionUUID = live.InteractionUUID
	}
	g.owners[key] = registration
	return true
}

// Live reports the live owner of one ticket+lane, if any.
func (g *CoordinationOwnerGuard) Live(ticket, lane string) (CoordinationOwnerRegistration, bool) {
	if g == nil {
		return CoordinationOwnerRegistration{}, false
	}
	key := CoordinationDedupeKey(ticket, lane)
	if key == "" {
		return CoordinationOwnerRegistration{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	live, ok := g.owners[key]
	return live, ok
}

// Release frees one ticket+lane (eg the owner finished), so the lane can be
// dispatched again. It reports whether an owner was actually released.
func (g *CoordinationOwnerGuard) Release(ticket, lane string) bool {
	if g == nil {
		return false
	}
	key := CoordinationDedupeKey(ticket, lane)
	if key == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.owners[key]; !ok {
		return false
	}
	delete(g.owners, key)
	return true
}

// Reconcile retires every owner whose interaction is no longer live and returns
// the released registrations, so the reconcile step of a dispatcher can hand the
// freed lanes back to the dispatcher (the recorded reason is
// CoordinationReasonReleased). An empty `liveInteractions` retires every owner:
// the caller that knows nothing about live work must not keep stale owners.
func (g *CoordinationOwnerGuard) Reconcile(liveInteractions []string) []CoordinationOwnerRegistration {
	if g == nil {
		return nil
	}
	live := map[string]bool{}
	for _, uuid := range liveInteractions {
		uuid = strings.TrimSpace(uuid)
		if uuid != "" {
			live[uuid] = true
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	released := []CoordinationOwnerRegistration{}
	for key, owner := range g.owners {
		if owner.InteractionUUID != "" && live[owner.InteractionUUID] {
			continue
		}
		released = append(released, owner)
		delete(g.owners, key)
	}
	sort.Slice(released, func(i, j int) bool { return released[i].OwnerKey < released[j].OwnerKey })
	return released
}

// Len is the number of live owners (one per ticket+lane).
func (g *CoordinationOwnerGuard) Len() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.owners)
}

// Snapshot returns the live owners sorted by owner key, so a listing is stable.
func (g *CoordinationOwnerGuard) Snapshot() []CoordinationOwnerRegistration {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	snapshot := make([]CoordinationOwnerRegistration, 0, len(g.owners))
	for _, owner := range g.owners {
		snapshot = append(snapshot, owner)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].OwnerKey < snapshot[j].OwnerKey })
	return snapshot
}

func registrationFromRequest(req CoordinationDispatchRequest, key string) CoordinationOwnerRegistration {
	return CoordinationOwnerRegistration{
		OwnerKey:        key,
		Ticket:          strings.TrimSpace(req.Ticket),
		Lane:            strings.TrimSpace(req.Lane),
		InteractionUUID: req.InteractionUUID,
		Source:          req.Source,
	}
}

// CoordinationTicketLaneTagPrefixes are the tag namespaces a dispatcher already
// uses to name the unit of work (`issue:540`) and its lane (`lane:coordinator`).
const (
	// CoordinationTicketTagPrefix names the ticket inside a dispatch tag list.
	CoordinationTicketTagPrefix = "issue:"

	// CoordinationLaneTagPrefix names the lane inside a dispatch tag list.
	CoordinationLaneTagPrefix = "lane:"
)

// CoordinationTicketAndLane resolves the (ticket, lane) of a dispatch from its
// explicit fields and, when they are absent, from its tags. The boolean reports
// whether the dispatch is owner-keyed at all, i.e. whether it named a ticket or a
// lane (explicitly or through a tag).
//
// A dispatch that names neither keeps its legacy behaviour: it is not
// owner-keyed, so the guard has nothing to derive and nothing to refuse. A
// dispatch that *does* name a lane but no ticket is owner-keyed with an empty
// ticket, which the guard then refuses - the caller must not invent an owner from
// a lane alone.
func CoordinationTicketAndLane(ticket, lane string, tags []string) (string, string, bool) {
	ticket = strings.TrimSpace(ticket)
	lane = strings.TrimSpace(lane)
	ownerKeyed := ticket != "" || lane != ""
	for _, raw := range tags {
		tag := normaliseCoordinationKeyPart(raw)
		switch {
		case strings.HasPrefix(tag, CoordinationTicketTagPrefix) && ticket == "":
			ticket = strings.TrimSpace(strings.TrimPrefix(tag, CoordinationTicketTagPrefix))
			ownerKeyed = true
		case strings.HasPrefix(tag, CoordinationLaneTagPrefix) && lane == "":
			lane = strings.TrimSpace(strings.TrimPrefix(tag, CoordinationLaneTagPrefix))
			ownerKeyed = true
		}
	}
	if !ownerKeyed {
		return "", "", false
	}
	return ticket, lane, true
}
