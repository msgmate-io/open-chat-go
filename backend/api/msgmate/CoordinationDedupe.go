package msgmate

import (
	"strings"
)

// ci#747 (RC7/QW4): the dispatcher must never produce a second interaction for
// a ticket that already has an owner. More than one interaction existed for the
// same ticket/lane (two ci#744 interactions: the monorepo PR #852 and a
// CI-config one), and the CLI view of in-flight work could not see them, so the
// lead could neither detect nor de-duplicate the duplicate.
//
// The owner key of one lane of one ticket is therefore a pure function of
// (ticket, lane) so every dispatcher path - the workflow route, the self-message
// schedule and the reconcile step - derives the *same* key for the same unit of
// work. Two dispatches of the same unit collapse onto one key; two different
// lanes of one ticket stay distinct (a lane is what parallelises a ticket).
const (
	// CoordinationLaneDefault is the lane of work that a ticket has before it is
	// split. An empty lane means "the default lane", never "any lane".
	CoordinationLaneDefault = "default"

	// CoordinationDedupeKeyPrefix namespaces the owner key so it cannot be
	// confused with a raw ticket reference.
	CoordinationDedupeKeyPrefix = "owner"
)

// CoordinationDedupeKey derives the stable owner key of one lane of one ticket.
//
// The result is lowercase and whitespace-normalised, so `ci#747`, `CI#747` and
// `ci#747 ` are the same owner - a duplicate that differs only in case is the
// common shape of the collisions observed on the tracker.
//
// An empty ticket yields an empty key: an unidentified dispatch cannot be
// de-duplicated and the caller must refuse it rather than invent an owner.
func CoordinationDedupeKey(ticket, lane string) string {
	ticket = normaliseCoordinationKeyPart(ticket)
	if ticket == "" {
		return ""
	}
	lane = normaliseCoordinationKeyPart(lane)
	if lane == "" {
		lane = CoordinationLaneDefault
	}
	return CoordinationDedupeKeyPrefix + ":" + ticket + ":" + lane
}

// normaliseCoordinationKeyPart lowercases a key part and collapses its internal
// whitespace, so cosmetic differences cannot masquerade as separate owners.
func normaliseCoordinationKeyPart(part string) string {
	return strings.Join(strings.Fields(strings.ToLower(part)), " ")
}

// SameCoordinationOwner reports whether two dispatches address the same lane of
// the same ticket, i.e. whether the second must be refused (or routed to the
// existing owner) instead of creating a duplicate interaction.
//
// Two unidentified dispatches never collide, so a caller that forgot the ticket
// is not silently treated as the owner of an unrelated lane.
func SameCoordinationOwner(ticketA, laneA, ticketB, laneB string) bool {
	a, b := CoordinationDedupeKey(ticketA, laneA), CoordinationDedupeKey(ticketB, laneB)
	return a != "" && a == b
}
