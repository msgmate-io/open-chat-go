package database

import (
	"testing"

	"gorm.io/gorm"
)

// SetupTestDatabase opens the database described by config for a test and
// registers the pool close that has to happen before t.TempDir removes the
// directory (ci#920). Use it instead of SetupDatabase in tests.
//
// t.TempDir registers its RemoveAll cleanup *before* this helper registers its
// own and testing runs cleanups LIFO, so the pool is already closed when the
// temporary directory is removed. Without it the pool outlives the test: SQLite
// runs in WAL mode (see sqliteDSNPragmas), so a pooled connection can recreate
// the -wal/-shm sidecars while RemoveAll is walking the directory. That races
// into "TempDir RemoveAll cleanup: unlinkat ...: directory not empty" and, once
// the database file is unlinked underneath a still-open connection, into
// "disk I/O error (522)" (observed flaking in CI).
func SetupTestDatabase(t *testing.T, config DBConfig) *gorm.DB {
	t.Helper()
	DB := SetupDatabase(config)
	CloseTestDB(t, DB)
	return DB
}

// CloseTestDB registers a t.Cleanup that closes the connection pool behind DB.
// It is a no-op for a nil handle and safe for a pool a site closes itself
// (database/sql Close is idempotent), so a helper that reopens the same file
// mid-test can still register it. Prefer SetupTestDatabase for new sites so the
// ordering rationale lives in exactly one place.
func CloseTestDB(t *testing.T, DB *gorm.DB) {
	t.Helper()
	if DB == nil {
		return
	}
	t.Cleanup(func() {
		sqlDB, err := DB.DB()
		if err != nil {
			return
		}
		if err := sqlDB.Close(); err != nil {
			t.Errorf("failed to close test database pool: %v", err)
		}
	})
}
