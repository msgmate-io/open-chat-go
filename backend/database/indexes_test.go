package database

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func setupIndexMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "index_migration_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return SetupTestDatabase(t, cfg)
}

// seedIndexMigrationRows inserts a small, deterministic dataset and refreshes
// the planner statistics. On an empty table SQLite happily picks any index -
// including the single-column `idx_messages_chat_id` AutoMigrate already
// creates - so a plan assertion without rows (or without ANALYZE) would pass or
// fail by accident instead of pinning the index the migration adds.
func seedIndexMigrationRows(t *testing.T, db *gorm.DB) {
	t.Helper()

	inserts := []string{
		"INSERT INTO messages (created_at, updated_at, chat_id, sender_id, receiver_id, data_type, deleted_at) " +
			"SELECT datetime('now'), datetime('now'), (x % 8) + 1, 1, 1, 'text', " +
			"CASE WHEN x % 7 = 0 THEN datetime('now') ELSE NULL END " +
			"FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 4000) SELECT x FROM c)",
		"INSERT INTO chats (created_at, updated_at, user1_id, user2_id, chat_type, latest_message_id) " +
			"SELECT datetime('now'), datetime('now'), x, x + 100000, 'conversation', x " +
			"FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 800) SELECT x FROM c)",
		"INSERT INTO streaming_messages (created_at, updated_at, chat_id, sender_id, receiver_id, session_id, seq) " +
			"SELECT datetime('now'), datetime('now'), (x % 8) + 1, 1, 1, 'sess-' || (x % 3), x % 5 " +
			"FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 2000) SELECT x FROM c)",
	}
	for _, stmt := range inserts {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seeding index migration test rows failed: %v", err)
		}
	}
	if err := db.Exec("ANALYZE").Error; err != nil {
		t.Fatalf("ANALYZE failed: %v", err)
	}
}

// explainQueryPlan returns the flattened `EXPLAIN QUERY PLAN` output for a query.
func explainQueryPlan(t *testing.T, db *gorm.DB, query string, args ...interface{}) string {
	t.Helper()

	rows, err := db.Raw("EXPLAIN QUERY PLAN "+query, args...).Rows()
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("failed to scan query plan row: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
	return plan.String()
}

// TestIndexMigrationCreatesHotPathIndexes verifies ci#850: every index declared
// for the slow chat/message list reads actually exists after the migration ran,
// and that a second run is a no-op (startup migrates on every boot).
func TestIndexMigrationCreatesHotPathIndexes(t *testing.T) {
	db := setupIndexMigrationTestDB(t)

	for _, idx := range hotPathIndexes {
		if !db.Migrator().HasIndex(idx.Table, idx.Name) {
			t.Fatalf("expected index %s on table %s after migration", idx.Name, idx.Table)
		}
	}

	// Re-running must stay green: the statements are CREATE INDEX IF NOT EXISTS
	// and the migration is part of the every-boot migration list.
	if err := (IndexMigration{}).Migrate(db); err != nil {
		t.Fatalf("second IndexMigration run failed: %v", err)
	}
}

// TestIndexMigrationSkipsMissingTables pins the partial-database behaviour: a
// secondary process without the external tables must not fail the migration.
func TestIndexMigrationSkipsMissingTables(t *testing.T) {
	db := setupIndexMigrationTestDB(t)
	if err := db.Exec("DROP TABLE streaming_messages").Error; err != nil {
		t.Fatalf("failed to drop streaming_messages: %v", err)
	}
	if err := (IndexMigration{}).Migrate(db); err != nil {
		t.Fatalf("IndexMigration must skip tables that do not exist: %v", err)
	}
}

// TestIndexMigrationUsesMessageChatSortIndex asserts the newest message page of
// a chat is served from the composite partial index instead of a scan + sort.
func TestIndexMigrationUsesMessageChatSortIndex(t *testing.T) {
	db := setupIndexMigrationTestDB(t)
	seedIndexMigrationRows(t, db)

	plan := explainQueryPlan(t, db,
		"SELECT * FROM messages WHERE chat_id = ? AND deleted_at IS NULL ORDER BY id DESC LIMIT 20",
		1,
	)
	if !strings.Contains(plan, "idx_messages_chat_deleted_id") {
		t.Fatalf("expected message list query to use idx_messages_chat_deleted_id, plan was:\n%s", plan)
	}
	if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("message list query still sorts in a temp b-tree, plan was:\n%s", plan)
	}
}

// TestIndexMigrationUsesCreatedAtIndex pins the cross-chat `created_at`
// subquery used by the chats/list `time_from`/`time_to` filters: it must be
// served by idx_messages_created_at (covering), not by a table scan.
func TestIndexMigrationUsesCreatedAtIndex(t *testing.T) {
	db := setupIndexMigrationTestDB(t)
	seedIndexMigrationRows(t, db)

	plan := explainQueryPlan(t, db,
		"SELECT id FROM messages WHERE created_at >= ?",
		"2000-01-01",
	)
	if !strings.Contains(plan, "idx_messages_created_at") {
		t.Fatalf("expected created_at subquery to use idx_messages_created_at, plan was:\n%s", plan)
	}
}

// TestIndexMigrationUsesStreamingChatSeqIndex pins the streaming state/resume
// lookups: streaming rows of a chat are read ordered by seq.
func TestIndexMigrationUsesStreamingChatSeqIndex(t *testing.T) {
	db := setupIndexMigrationTestDB(t)
	seedIndexMigrationRows(t, db)

	plan := explainQueryPlan(t, db,
		"SELECT * FROM streaming_messages WHERE chat_id = ? ORDER BY seq LIMIT 5",
		1,
	)
	if !strings.Contains(plan, "idx_streaming_messages_chat_seq") {
		t.Fatalf("expected streaming state query to use idx_streaming_messages_chat_seq, plan was:\n%s", plan)
	}
}

// TestIndexMigrationUsesStreamingSessionSeqIndex pins the streaming resume
// lookup by session id.
func TestIndexMigrationUsesStreamingSessionSeqIndex(t *testing.T) {
	db := setupIndexMigrationTestDB(t)
	seedIndexMigrationRows(t, db)

	plan := explainQueryPlan(t, db,
		"SELECT * FROM streaming_messages WHERE session_id = ? ORDER BY seq LIMIT 5",
		"sess-1",
	)
	if !strings.Contains(plan, "idx_streaming_messages_session_seq") {
		t.Fatalf("expected streaming resume query to use idx_streaming_messages_session_seq, plan was:\n%s", plan)
	}
}

// TestIndexMigrationChatMembershipIndex documents the ci#850 finding that the
// chats list membership filter (`user1_id = ? OR user2_id = ?`) is already
// served by the single-column indexes AutoMigrate creates through the
// `gorm:"index"` tags - the composite `chats(user1_id|user2_id, id DESC)`
// candidates that were tried first were never chosen by the planner (SQLite
// still merges the two scans with MULTI-INDEX OR and sorts afterwards) and were
// therefore dropped from hotPathIndexes. This test fails if a future schema
// change loses that coverage, which would mean the OR rewrite at the query
// layer is due (see rejectedIndexCandidates).
func TestIndexMigrationChatMembershipIndex(t *testing.T) {
	db := setupIndexMigrationTestDB(t)
	seedIndexMigrationRows(t, db)

	plan := explainQueryPlan(t, db,
		"SELECT * FROM chats WHERE user1_id = ? OR user2_id = ? ORDER BY id DESC LIMIT 20",
		1, 1,
	)
	// AutoMigrate's single-column indexes are the ones the planner picks.
	if !strings.Contains(plan, "idx_chats_user1_id") && !strings.Contains(plan, "idx_chats_user2_id") {
		t.Fatalf("expected chats membership query to use the user1_id/user2_id indexes, plan was:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN chats") {
		t.Fatalf("chats membership query degrades to a full table scan, plan was:\n%s", plan)
	}
}

// TestIndexMigrationRejectsUnusedCompositeIndexes keeps the ci#850 negative
// results honest: the candidates listed in rejectedIndexCandidates must not be
// (re)introduced by hotPathIndexes, because the planner ignores them and an
// unused index is pure write amplification + WAL churn on the SQLite backend.
func TestIndexMigrationRejectsUnusedCompositeIndexes(t *testing.T) {
	for _, rejected := range rejectedIndexCandidates {
		if rejected == "" {
			continue
		}
		for _, idx := range hotPathIndexes {
			if idx.SQL == rejected || strings.Contains(idx.SQL, rejected) {
				t.Fatalf("rejected index candidate %q is declared in hotPathIndexes (%s)", rejected, idx.Name)
			}
		}
	}
}
