package database

import (
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// detachedWorkDrainTimeout bounds how long a test teardown waits for the detached
// DB work of the packages that registered a group (see RegisterDetachedWork) and
// for the SQLite pool to go quiescent. A goroutine that never returns must not hang
// the test binary, so the wait is bounded and the failure is reported instead
// (ci#811).
const detachedWorkDrainTimeout = 10 * time.Second

// sqliteBusyRetryTimeout bounds the retry loop around a pool close that SQLite
// refuses while another connection still runs a statement.
const sqliteBusyRetryTimeout = 5 * time.Second

// SetupTestDatabase opens the database described by config for a test and
// registers the pool close that has to happen before t.TempDir removes the
// directory. Use it instead of SetupDatabase in tests.
//
// t.TempDir registers its RemoveAll cleanup *before* this helper registers its
// own and testing runs cleanups LIFO, so the pool is already closed when the
// temporary directory is removed. Without it the pool outlives the test: SQLite
// runs in WAL mode (see sqliteDSNPragmas), so a pooled connection can recreate
// the -wal/-shm sidecars while RemoveAll is walking the directory. That races
// into "TempDir RemoveAll cleanup: unlinkat ...: directory not empty" and, once
// the database file is unlinked underneath a still-open connection, into
// "disk I/O error (522)" (observed flaking in CI).
//
// ci#811: closing the pool is only half the handshake. A handler that detaches a
// goroutine which keeps using the pool (backend/api/chats does that for its
// best-effort notifications) can write *after* the close, or reopen the SQLite
// file, and t.TempDir then fails with "directory not empty" again. The cleanup
// below therefore waits for the registered detached work first, then for the
// pool itself to go quiescent. A package that hands a DB to a goroutine must
// register its group with RegisterDetachedWork.
func SetupTestDatabase(t *testing.T, config DBConfig) *gorm.DB {
	t.Helper()
	DB := SetupDatabase(config)
	CloseTestDB(t, DB)
	return DB
}

// CloseTestDB registers a t.Cleanup that drains the detached DB work, waits for
// the pool behind DB to go quiescent and only then closes it (ci#811, see
// detachedWorkDrainTimeout).
//
// The wait happens at cleanup time, not at registration time: the goroutine a test
// triggers by driving a handler does not exist yet when the helper is called, and
// a check that ran there would always see an idle group - exactly the ci#920-style
// gap that let this case regress. A helper that reopens the same file mid-test can
// still register the pool: database/sql Close is idempotent. Prefer
// SetupTestDatabase for new sites so the ordering rationale lives in one place.
func CloseTestDB(t *testing.T, DB *gorm.DB) {
	t.Helper()
	if DB == nil {
		return
	}
	t.Cleanup(func() {
		if !DrainRegisteredDetachedWork(detachedWorkDrainTimeout) {
			t.Errorf("ci#811: detached database work started by another goroutine was still running %s after the "+
				"test returned; the pool is closed next, so a late write would race the t.TempDir cleanup. "+
				"Route that work through database.DetachedWork and RegisterDetachedWork",
				detachedWorkDrainTimeout)
		}
		if !WaitForTestDBQuiescent(t, DB, detachedWorkDrainTimeout) {
			t.Errorf("ci#811: a database handle still held an open connection %s after the test returned, so "+
				"closing it (and the t.TempDir RemoveAll that follows) is not safe", detachedWorkDrainTimeout)
		}
		CloseTestDBQuietly(t, DB, true)
	})
}

// WaitForTestDBQuiescent waits (bounded by timeout) until every connection the DB
// has handed out has come back to the pool and reports whether it succeeded. A
// goroutine that is between two statements holds no connection at the instant it
// is checked, so this is a backstop for the pool state, not a substitute for
// draining the goroutines themselves (ci#811).
func WaitForTestDBQuiescent(t *testing.T, DB *gorm.DB, timeout time.Duration) bool {
	t.Helper()
	if DB == nil {
		return true
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return true
	}
	deadline := time.Now().Add(timeout)
	for {
		stats := sqlDB.Stats()
		if stats.InUse == 0 {
			return true
		}
		if timeout > 0 && !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// CloseTestDBQuietly closes the pool behind DB, retrying the close while SQLite
// reports the database is busy (a statement is still running on another
// connection). It reports whether the pool is closed afterwards. Unlike
// CloseTestDB it registers nothing, so a site that owns its teardown ordering can
// call it directly, e.g. right after a table-driven subtest closes and reopens the
// same file. With failTests it reports a bounded failure with t.Errorf once
// sqliteBusyRetryTimeout is exhausted; a test that turned t into a cleanup-time
// no-op must not fail another test through t.Fatalf.
func CloseTestDBQuietly(t *testing.T, DB *gorm.DB, failTests bool) bool {
	if DB == nil {
		return true
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return true
	}

	deadline := time.Now().Add(sqliteBusyRetryTimeout)
	for {
		// database/sql Close is idempotent: a pool a site already closed stays
		// closed and the busy retry below only ever runs while it is open.
		closeErr := sqlDB.Close()
		if closeErr == nil {
			return true
		}
		if !isSQLiteBusyError(closeErr) {
			if t != nil && failTests {
				t.Errorf("failed to close test database pool (ci#811): %v", closeErr)
			}
			return false
		}
		if !time.Now().Before(deadline) {
			if t != nil && failTests {
				t.Errorf("failed to close test database pool (ci#811): SQLite reported the database as busy for %s: %v",
					sqliteBusyRetryTimeout, closeErr)
			}
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// isSQLiteBusyError matches the lock contention SQLite reports when a close races
// another connection's statement. The driver surfaces them as plain errors, so the
// message is all there is to match on.
func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database is busy") ||
		strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "database table is locked")
}
