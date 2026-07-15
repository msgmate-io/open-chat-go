package database

import (
	"fmt"

	"backend/logging"

	"gorm.io/gorm"
)

// IndexMigration is a small, idempotent index migration that covers the
// filter + sort column combinations of the hot read paths (ci#850). GORM's
// AutoMigrate only creates the plain single-column indexes declared through
// `gorm:"index"` tags, which are not enough for the composite/partial access
// patterns used by the chat and message list endpoints.
//
// The index set below was chosen from `EXPLAIN QUERY PLAN` runs over a
// production-shaped SQLite dataset (production DB is ~1.7 GB, SQLite
// backend), not from the declared tags. Every declared index is pinned by
// backend/database/indexes_test.go so an index the planner never picks cannot
// stay behind: candidates that were tried and *not* selected are documented
// under rejectedIndexCandidates.
type IndexMigration struct{}

// indexStatement is one idempotent index creation statement plus a human
// readable purpose used for logging.
type indexStatement struct {
	Name  string
	Table string
	SQL   string
	Why   string
}

// hotPathIndexes lists the indexes added for the ci#850 slow chat/message list
// reads. Partial indexes (`WHERE deleted_at IS NULL`) are supported by both
// SQLite (>= 3.8) and PostgreSQL (>= 9.5), the two backends this project runs.
//
// The names deliberately avoid the ones AutoMigrate already owns: the
// `gorm:"index"` tags create `idx_messages_chat_id` and
// `idx_chats_user1_id`/`idx_chats_user2_id` (all single column), so a
// composite index reusing one of those names would be skipped by the
// `HasIndex` guard below and never created.
//
// Verified plans (SQLite, after seeding rows + ANALYZE):
//
//	GET /chats/{uuid}/messages/list
//	  -> SEARCH messages USING INDEX idx_messages_chat_deleted_id (chat_id=?)
//	     with no "USE TEMP B-TREE FOR ORDER BY": the composite partial index
//	     satisfies both the `chat_id = ? AND deleted_at IS NULL` filter and the
//	     `ORDER BY id DESC` sort in one range scan.
//	messages(created_at) subquery (chats/list time_from/time_to)
//	  -> SEARCH messages USING COVERING INDEX idx_messages_created_at
//	streaming state/resume lookups
//	  -> SEARCH streaming_messages USING INDEX idx_streaming_messages_chat_seq
//	     / idx_streaming_messages_session_seq
var hotPathIndexes = []indexStatement{
	{
		Name:  "idx_messages_chat_deleted_id",
		Table: "messages",
		SQL:   "CREATE INDEX IF NOT EXISTS idx_messages_chat_deleted_id ON messages(chat_id, id DESC) WHERE deleted_at IS NULL",
		Why:   "messages/list: chat_id filter + id DESC sort without soft-deleted rows",
	},
	{
		Name:  "idx_messages_created_at",
		Table: "messages",
		SQL:   "CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)",
		Why:   "chats/list time_from/time_to: latest_message_id IN (messages by created_at)",
	},
	{
		Name:  "idx_chats_chat_type_latest_message",
		Table: "chats",
		SQL:   "CREATE INDEX IF NOT EXISTS idx_chats_chat_type_latest_message ON chats(chat_type, latest_message_id, id DESC)",
		Why:   "chats/list: chat_type filter plus latest-message ordering",
	},
	{
		Name:  "idx_streaming_messages_chat_seq",
		Table: "streaming_messages",
		SQL:   "CREATE INDEX IF NOT EXISTS idx_streaming_messages_chat_seq ON streaming_messages(chat_id, seq)",
		Why:   "chat state/interaction status: streaming rows of a chat ordered by seq",
	},
	{
		Name:  "idx_streaming_messages_session_seq",
		Table: "streaming_messages",
		SQL:   "CREATE INDEX IF NOT EXISTS idx_streaming_messages_session_seq ON streaming_messages(session_id, seq)",
		Why:   "streaming resume: session_id lookup ordered by seq",
	},
}

// rejectedIndexCandidates records what the ci#850 investigation tried and
// discarded, so the same candidates are not re-added without new evidence.
// Each entry was measured with a before/after `EXPLAIN QUERY PLAN` on the same
// seeded dataset; a candidate is dropped when the plan is identical with and
// without it (an unused index only costs write amplification and WAL churn,
// the two things that amplify the SQLITE_BUSY contention this issue tracks).
//
//   - `messages(chat_id, created_at)`: never chosen. The time-range/watermark
//     probes (`chat_id = ? AND created_at >= ? [ORDER BY created_at DESC]`)
//     always resolve through the single-column `idx_messages_chat_id`, with or
//     without this index. The cross-chat `created_at` subquery is served by
//     idx_messages_created_at as a covering index.
//   - `chats(user1_id, id DESC)` / `chats(user2_id, id DESC)`: never chosen.
//     `user1_id = ? OR user2_id = ? ORDER BY id DESC` keeps resolving to
//     `MULTI-INDEX OR` over the AutoMigrate-created single-column
//     `idx_chats_user1_id`/`idx_chats_user2_id`, because SQLite's OR
//     optimization must merge two index scans and sort the result anyway, so
//     the wider `_desc` variants buy nothing. The membership filter was
//     therefore already indexed for this deployment size; the real fix for a
//     very chat-heavy account would be a union-all rewrite at the query layer,
//     not another index.
var rejectedIndexCandidates = []string{
	"messages(chat_id, created_at)",
	"chats(user1_id, id DESC)",
	"chats(user2_id, id DESC)",
}

// Migrate creates the hot-path indexes. Missing tables are skipped so the
// migration stays usable on partially migrated databases (eg. a secondary
// process that only has the external tables).
func (IndexMigration) Migrate(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	for _, idx := range hotPathIndexes {
		if !db.Migrator().HasTable(idx.Table) {
			logging.Debugf("Skipping index %s: table %s does not exist", idx.Name, idx.Table)
			continue
		}
		if db.Migrator().HasIndex(idx.Table, idx.Name) {
			continue
		}
		logging.Infof("Creating index %s on %s (%s)", idx.Name, idx.Table, idx.Why)
		if err := db.Exec(idx.SQL).Error; err != nil {
			return fmt.Errorf("failed to create index %s on %s: %w", idx.Name, idx.Table, err)
		}
	}
	return nil
}
