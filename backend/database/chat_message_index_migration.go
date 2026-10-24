package database

import (
	"fmt"

	"backend/logging"

	"gorm.io/gorm"
)

// indexColumnSpec describes one composite index the chat/message read paths
// depend on. The name is stable because the migration only ever issues
// `CREATE INDEX IF NOT EXISTS`, so a second run (or a second pod rolling at the
// same time) is a no-op instead of an error.
type indexColumnSpec struct {
	Name    string
	Table   string
	Columns string
	// Rationale is kept next to the DDL so the next person can check whether an
	// index is still needed instead of dropping it because "it looked unused".
	Rationale string
}

// chatMessageReadIndexes are the indexes that cover the query shapes of the
// chat/message read endpoints (ci#850):
//
//   - GET /chats/list           -> chats WHERE user1_id = ? OR user2_id = ?
//     ORDER BY id DESC LIMIT n  (plus a chat_pins lookup per user)
//   - GET /chats/{uuid}/messages/list
//     -> messages WHERE chat_id = ? AND deleted_at IS NULL ORDER BY id DESC
//   - GET /chats/{uuid}/messages/streaming
//     -> streaming_messages WHERE chat_id = ? ORDER BY updated_at DESC LIMIT 1
//   - account_states lookups per user (email verification / suspension checks)
//
// The single-column indexes GORM's model tags used to create only cover the
// filter, not the sort key, so SQLite fell back to "USE TEMP B-TREE FOR ORDER
// BY" over every row of the chat/user before applying LIMIT. The composite
// indexes below let the planner walk the index in the requested order and stop
// at the page size.
var chatMessageReadIndexes = []indexColumnSpec{
	{
		Name:      "idx_messages_chat_id_id",
		Table:     "messages",
		Columns:   "chat_id, id DESC",
		Rationale: "message list pages one chat ordered by id DESC",
	},
	{
		Name:      "idx_chats_user1_id_id",
		Table:     "chats",
		Columns:   "user1_id, id DESC",
		Rationale: "chat list OR-branch 1 plus ORDER BY id DESC",
	},
	{
		Name:      "idx_chats_user2_id_id",
		Table:     "chats",
		Columns:   "user2_id, id DESC",
		Rationale: "chat list OR-branch 2 plus ORDER BY id DESC",
	},
	{
		Name:      "idx_chat_pins_user_id_chat_id",
		Table:     "chat_pins",
		Columns:   "user_id, chat_id",
		Rationale: "chat list resolves pinned chat ids by user; the unique index is (chat_id, user_id) and therefore cannot serve a user_id prefix",
	},
	{
		Name:      "idx_streaming_messages_chat_id_updated_at",
		Table:     "streaming_messages",
		Columns:   "chat_id, updated_at DESC",
		Rationale: "streaming snapshot lookup ordered by updated_at DESC",
	},
	{
		Name:      "idx_account_states_user_id",
		Table:     "account_states",
		Columns:   "user_id",
		Rationale: "email verification / suspension checks look the row up by user_id (table is owned by the account_management integration)",
	},
}

// ChatMessageIndexMigration adds the composite indexes behind the slow
// chat/message list reads (ci#850). It is intentionally DDL-only and idempotent:
// `CREATE INDEX IF NOT EXISTS` is supported by both SQLite and Postgres, and a
// table that is not part of this build (eg. account_states comes from the
// account_management integration) is skipped instead of failing the startup
// migration pass.
type ChatMessageIndexMigration struct{}

func (ChatMessageIndexMigration) Migrate(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	created := 0
	for _, index := range chatMessageReadIndexes {
		if !db.Migrator().HasTable(index.Table) {
			continue
		}
		statement := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", index.Name, index.Table, index.Columns)
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("failed to create index %s on %s (ci#850): %v", index.Name, index.Table, err)
		}
		created++
		logging.Infof("ci#850: ensured index %s on %s(%s) - %s", index.Name, index.Table, index.Columns, index.Rationale)
	}
	if created > 0 {
		logging.Infof("ci#850: chat/message read indexes ensured (%d)", created)
	}
	return nil
}

// chatMessageReadIndexNames lists the index names ChatMessageIndexMigration is
// responsible for. Exposed for tests and for the operational doc so the
// migration and its verification cannot drift apart.
func chatMessageReadIndexNames() []string {
	names := make([]string, 0, len(chatMessageReadIndexes))
	for _, index := range chatMessageReadIndexes {
		names = append(names, index.Name)
	}
	return names
}
