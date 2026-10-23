// Package coordination holds the shared vocabulary the dispatcher, the
// self-message scheduler and the reconcile step use to identify one unit of
// coordination work (ci#747 RC7/QW4).
//
// It is deliberately a leaf package (no repository imports) so every layer can
// derive the *same* owner key for the same unit of work without an import cycle
// and without a second, drifting definition of "owner".
package coordination

import "strings"

const (
	// LaneDefault is the lane of work a ticket has before it is split. An empty
	// lane means "the default lane", never "any lane".
	LaneDefault = "default"

	// OwnerKeyPrefix namespaces the owner key so it cannot be confused with a
	// raw ticket reference.
	OwnerKeyPrefix = "owner"
)

// OwnerKey derives the stable owner key of one lane of one ticket.
//
// The result is lowercase and whitespace-normalised, so `ci#747`, `CI#747` and
// `ci#747 ` are the same owner - a duplicate that differs only in case is the
// common shape of the collisions observed on the tracker. An empty ticket yields
// an empty key: an unidentified dispatch cannot be de-duplicated, so the caller
// must refuse it rather than invent an owner.
func OwnerKey(ticket, lane string) string {
	ticket = normaliseKeyPart(ticket)
	if ticket == "" {
		return ""
	}
	lane = normaliseKeyPart(lane)
	if lane == "" {
		lane = LaneDefault
	}
	return OwnerKeyPrefix + ":" + ticket + ":" + lane
}

// OwnerKeyTag renders the owner key as the interaction tag the dispatcher
// stamps, so a later dispatch of the same lane finds the existing owner through
// the same tag lookup the other dedupe keys already use (ci#625).
func OwnerKeyTag(ticket, lane string) string {
	return OwnerKey(ticket, lane)
}

// SameOwner reports whether two dispatches address the same lane of the same
// ticket, i.e. whether the second must be refused (or routed to the existing
// owner) instead of creating a duplicate interaction. Two unidentified
// dispatches never collide, so a caller that forgot the ticket is not silently
// treated as the owner of an unrelated lane.
func SameOwner(ticketA, laneA, ticketB, laneB string) bool {
	a, b := OwnerKey(ticketA, laneA), OwnerKey(ticketB, laneB)
	return a != "" && a == b
}

// TicketFromTags recovers the (ticket, lane) unit of work from an interaction's
// tags for the reconcile step: the ticket is the first canonical ticket tag
// (`issue:`, `ticket:` or a bare `ci#`/`gh#` reference) and the lane is the
// `lane:` tag, defaulting to LaneDefault. It returns an empty ticket when the
// tags name no unit of work, so reconcile can report the interaction as
// unidentified instead of guessing an owner.
func TicketFromTags(tags []string) (string, string) {
	ticket, lane := "", ""
	for _, tag := range tags {
		value := strings.TrimSpace(strings.ToLower(tag))
		if value == "" {
			continue
		}
		switch {
		case ticket == "" && strings.HasPrefix(value, "issue:"):
			ticket = strings.TrimSpace(strings.TrimPrefix(value, "issue:"))
		case ticket == "" && strings.HasPrefix(value, "ticket:"):
			ticket = strings.TrimSpace(strings.TrimPrefix(value, "ticket:"))
		case ticket == "" && (strings.HasPrefix(value, "ci#") || strings.HasPrefix(value, "gh#")):
			ticket = value
		case lane == "" && strings.HasPrefix(value, "lane:"):
			lane = strings.TrimSpace(strings.TrimPrefix(value, "lane:"))
		}
	}
	if ticket == "" {
		return "", ""
	}
	if lane == "" {
		lane = LaneDefault
	}
	return ticket, lane
}

// OwnerKeyFromTags recovers the owner key of an interaction for the reconcile
// step. It returns an empty key unless the interaction actually participates in
// lane ownership - it carries an explicit owner key tag, or an explicit lane tag
// together with a ticket - so an un-laned interaction is never mistaken for a
// competing owner of the default lane.
func OwnerKeyFromTags(tags []string) string {
	for _, tag := range tags {
		value := strings.TrimSpace(strings.ToLower(tag))
		if strings.HasPrefix(value, OwnerKeyPrefix+":") {
			return value
		}
	}
	if !HasLaneTag(tags) {
		return ""
	}
	ticket, lane := TicketFromTags(tags)
	return OwnerKey(ticket, lane)
}

// HasLaneTag reports whether the tags explicitly name a lane. An explicit lane
// is the opt-in that turns (ticket, lane) ownership into a hard gate: a dispatch
// that declares a lane claims that lane exclusively, while an un-laned dispatch
// keeps the historical behaviour (only an explicit DedupeKey de-duplicates) so
// the lead can still run several un-laned workers for one ticket.
func HasLaneTag(tags []string) bool {
	for _, tag := range tags {
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(tag)), "lane:") {
			return true
		}
	}
	return false
}

// normaliseKeyPart lowercases a key part and collapses its internal whitespace,
// so cosmetic differences cannot masquerade as separate owners.
func normaliseKeyPart(part string) string {
	return strings.Join(strings.Fields(strings.ToLower(part)), " ")
}
