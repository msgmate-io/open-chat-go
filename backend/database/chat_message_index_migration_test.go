package database

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// ci#850: the chat/message read endpoints were slow (1.9-7s) and the DB saw
// "database is locked" errors. The schema side of that fix is the idempotent
// ChatMessageIndexMigration, which adds composite read indexes that GORM's
// single-column model tags do not create. These tests pin that migration and the
// query-plan improvements it is responsible for, so a later schema change that
// drops an index fails here instead of silently turning into latency.
//
// The plan assertions below deliberately only cover the query shapes where the
// composite index removes the sort for good (streaming snapshot) or turns a full
// table scan into an index lookup (chat_pins). SQLite decides between a
// chat_id-leading single-column index and a composite one from its row-count
// statistics, so for the OR-predicated chat list the sort is only removed by the
// UNION ALL rewrite that follows in the ticket's second step (query changes),
// not by an index - asserting otherwise here would make the test flaky.

// sqliteIndexExists reports whether a named index exists in the SQLite schema.
func sqliteIndexExists(t *testing.T, DB *gorm.DB, name string) bool {
	t.Helper()
	var count int64
	if err := DB.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name).
		Scan(&count).Error; err != nil {
		t.Fatalf("failed to look up index %s: %v", name, err)
	}
	return count > 0
}

// sqliteQueryPlan runs EXPLAIN QUERY PLAN and returns the flattened detail lines,
// which is what the assertions below match on.
func sqliteQueryPlan(t *testing.T, DB *gorm.DB, query string, args ...interface{}) string {
	t.Helper()
	rows, err := DB.Raw("EXPLAIN QUERY PLAN "+query, args...).Rows()
	if err != nil {
		t.Fatalf("failed to explain %q: %v", query, err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("failed to scan query plan for %q: %v", query, err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("failed to read query plan for %q: %v", query, err)
	}
	plan := strings.Join(details, "; ")
	t.Logf("plan for %q: %s", query, plan)
	return plan
}

// setupIndexTestDB opens a fresh migrated SQLite database. account_states is not
// part of this build's migrations (it belongs to the account_management
// integration), so createAccountStates optionally creates it to cover both the
// "index the real table" and the "skip a missing table" branch.
func setupIndexTestDB(t *testing.T, createAccountStates bool) *gorm.DB {
	t.Helper()
	DB := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "chat_message_indexes.db"),
		ResetDB:  true,
	})
	if createAccountStates {
		if err := DB.Exec("CREATE TABLE IF NOT EXISTS account_states (user_id integer)").Error; err != nil {
			t.Fatalf("failed to create account_states: %v", err)
		}
	}
	return DB
}

func TestChatMessageIndexMigrationCreatesReadIndexes(t *testing.T) {
	DB := setupIndexTestDB(t, true)

	if err := (ChatMessageIndexMigration{}).Migrate(DB); err != nil {
		t.Fatalf("ChatMessageIndexMigration.Migrate failed: %v", err)
	}
	for _, name := range chatMessageReadIndexNames() {
		if !sqliteIndexExists(t, DB, name) {
			t.Errorf("expected index %s to exist after the migration", name)
		}
	}

	// The migration runs on every pod start, so a second run must be a no-op
	// rather than a duplicate-index error.
	if err := (ChatMessageIndexMigration{}).Migrate(DB); err != nil {
		t.Fatalf("second ChatMessageIndexMigration.Migrate run must be a no-op: %v", err)
	}
}

// A table owned by an integration that is not part of this build must be skipped
// instead of failing: the migration runs inside the startup pass, and an error
// there keeps the process from coming up.
func TestChatMessageIndexMigrationSkipsMissingIntegrationTable(t *testing.T) {
	DB := setupIndexTestDB(t, false)

	if err := (ChatMessageIndexMigration{}).Migrate(DB); err != nil {
		t.Fatalf("migration must skip a missing table instead of failing: %v", err)
	}
	if sqliteIndexExists(t, DB, "idx_account_states_user_id") {
		t.Errorf("index idx_account_states_user_id must not be created without its table")
	}
	// The tables that do exist must still have been indexed.
	for _, name := range []string{"idx_messages_chat_id_id", "idx_chat_pins_user_id_chat_id", "idx_streaming_messages_chat_id_updated_at"} {
		if !sqliteIndexExists(t, DB, name) {
			t.Errorf("expected index %s on a table that exists", name)
		}
	}
}

// The streaming snapshot endpoint takes the newest row per chat with
// ORDER BY updated_at DESC LIMIT 1. Without the composite index SQLite scans the
// chat's rows and sorts them ("USE TEMP B-TREE FOR ORDER BY"); with it the row is
// read straight off the index.
func TestStreamingSnapshotQueryUsesCompositeIndex(t *testing.T) {
	DB := setupIndexTestDB(t, false)

	for i := 0; i < 200; i++ {
		if err := DB.Exec(
			"INSERT INTO streaming_messages (created_at, updated_at, chat_id, sender_id) VALUES (datetime('now'), datetime('now'), ?, ?)",
			1, 1,
		).Error; err != nil {
			t.Fatalf("failed to seed streaming snapshot %d: %v", i, err)
		}
	}

	plan := sqliteQueryPlan(t, DB,
		"SELECT * FROM streaming_messages WHERE chat_id = ? ORDER BY updated_at DESC LIMIT 1", 1)
	if !strings.Contains(plan, "idx_streaming_messages_chat_id_updated_at") {
		t.Errorf("streaming snapshot lookup does not use the composite index: %s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Errorf("streaming snapshot lookup still sorts through a temp b-tree: %s", plan)
	}
}

// The chat-list pinned top-up looks pins up by user_id. The existing unique index
// is (chat_id, user_id), so without the added composite index that lookup scans
// the whole chat_pins table.
func TestChatPinsUserLookupUsesUserIndex(t *testing.T) {
	DB := setupIndexTestDB(t, false)

	for i := 0; i < 200; i++ {
		if err := DB.Exec(
			"INSERT INTO chat_pins (created_at, updated_at, chat_id, user_id) VALUES (datetime('now'), datetime('now'), ?, ?)",
			i+1, 7,
		).Error; err != nil {
			t.Fatalf("failed to seed chat pin %d: %v", i, err)
		}
	}

	plan := sqliteQueryPlan(t, DB, "SELECT chat_id FROM chat_pins WHERE user_id = ?", 7)
	if !strings.Contains(plan, "idx_chat_pins_user_id_chat_id") {
		t.Errorf("pin lookup does not use idx_chat_pins_user_id_chat_id: %s", plan)
	}
	if strings.Contains(plan, "SCAN chat_pins") {
		t.Errorf("pin lookup scans the whole chat_pins table: %s", plan)
	}
}
