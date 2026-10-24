package chats

// MaxMessageListLimit caps how many messages a single message-list request may
// return. Without a cap a client can ask for the whole transcript in one query,
// which scans the largest table in the database and competes with the single
// writer for the SQLite lock (ci#850). 100 is the largest transcript window the
// UI renders in one batch, so the cap is invisible to clients that page.
const MaxMessageListLimit = 100

// messageListSort is the deterministic message order shared by every
// message-list endpoint: newest first, with id breaking ties so a cursor never
// skips or repeats a row that shares a created_at tick (ci#850).
const messageListSort = "created_at desc, id desc"

// clampMessageListLimit bounds a caller-supplied limit to MaxMessageListLimit.
// Non-positive limits are normalised to the pagination default by the caller and
// never reach this helper.
func clampMessageListLimit(limit int) int {
	if limit > MaxMessageListLimit {
		return MaxMessageListLimit
	}
	return limit
}
