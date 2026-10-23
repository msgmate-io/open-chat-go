package bots

import "backend/coordination"

// ownerKeyForDispatch derives the dispatch identity the dedupe gate enforces
// (ci#747 RC7/QW4).
//
// An explicit DedupeKey keeps its historical meaning and always enforces, so the
// stable lead tag (`lead:open-chat-go-ci`) de-duplicates exactly as before
// (ci#625). Without one, ownership is derived from the ticket and an *explicit*
// lane tag: declaring a lane claims that lane of that ticket exclusively, so the
// same lane cannot be dispatched twice by two automated actors - the shape that
// left two interactions on one ticket (ci#744).
//
// The lane tag is the opt-in on purpose. An un-laned dispatch keeps the
// historical behaviour, so the lead can still run several un-laned workers for
// one ticket, and a dispatch that names no ticket is never de-duplicated: it must
// not silently adopt an unrelated lane.
//
// The gate only ever matches a *live* interaction (liveInteractionForDedupeKey),
// and `force: true` remains the documented recovery route: when the existing
// owner is stalled, the re-dispatch sets force so the lane gets a working owner
// again instead of being swallowed by its own owner key.
func ownerKeyForDispatch(tags []string, explicitDedupeKey string) (key string, enforce bool) {
	if key = dedupeKeyTag(explicitDedupeKey); key != "" {
		return key, true
	}
	if !coordination.HasLaneTag(tags) {
		return "", false
	}
	ticket, lane := coordination.TicketFromTags(tags)
	key = coordination.OwnerKeyTag(ticket, lane)
	if key == "" {
		return "", false
	}
	return key, true
}
