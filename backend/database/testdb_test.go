package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestSetupTestDatabaseClosesPoolWhenTestEnds pins the ordering the helper
// exists for (ci#920): the pool close is registered after t.TempDir, so it runs
// first and RemoveAll finds a quiesced directory instead of racing the WAL
// sidecars.
func TestSetupTestDatabaseClosesPoolWhenTestEnds(t *testing.T) {
	var sqlDB *sql.DB

	t.Run("inner", func(t *testing.T) {
		DB := SetupTestDatabase(t, DBConfig{
			Backend:  "sqlite",
			FilePath: filepath.Join(t.TempDir(), "setup_test_database.db"),
			ResetDB:  true,
		})
		if DB == nil {
			t.Fatal("expected a database handle")
		}

		var err error
		sqlDB, err = DB.DB()
		if err != nil {
			t.Fatalf("failed to unwrap sql.DB: %v", err)
		}
		if err := sqlDB.Ping(); err != nil {
			t.Fatalf("pool should be open inside the test: %v", err)
		}
	})

	if err := sqlDB.Ping(); err == nil {
		t.Fatal("expected the pool to be closed once the test ended (ci#920)")
	}
}

// TestCloseTestDBToleratesASiteClosingItsOwnPool documents that a site which
// closes its pool explicitly can still register the helper: database/sql Close
// is idempotent, so there is no double-close error.
func TestCloseTestDBToleratesASiteClosingItsOwnPool(t *testing.T) {
	DB := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "close_test_db.db"),
		ResetDB:  true,
	})

	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("failed to unwrap sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("second close must be a no-op, got: %v", err)
	}
}
