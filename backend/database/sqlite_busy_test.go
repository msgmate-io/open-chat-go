package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openBusyTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	DB, err := gorm.Open(sqlite.Open(sqliteDSN(path)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	return DB
}

func insertLockedWrite(db *gorm.DB, value string) error {
	return db.Exec("INSERT INTO locked_writes (value) VALUES (?)", value).Error
}

// lockSQLiteFileHeldByOtherPod takes the SQLite write lock through an
// independent connection and returns the release function, mirroring the
// previous pod that is still shutting down on the shared volume (ci#679).
func lockSQLiteFileHeldByOtherPod(t *testing.T, holder *gorm.DB) func() {
	t.Helper()
	if err := holder.Exec("CREATE TABLE locked_writes (id INTEGER PRIMARY KEY, value TEXT)").Error; err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	pool, err := holder.DB()
	if err != nil {
		t.Fatalf("failed to unwrap sql.DB: %v", err)
	}
	pool.SetMaxOpenConns(1)
	conn, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatalf("failed to reserve the blocking connection: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		t.Fatalf("failed to take the SQLite write lock: %v", err)
	}

	return func() {
		if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
			t.Errorf("failed to release the SQLite write lock: %v", err)
		}
		conn.Close()
	}
}

func TestIsSQLiteBusyError(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if IsSQLiteBusyError(nil) {
			t.Fatalf("nil must not be reported as a lock error")
		}
	})

	t.Run("real driver lock error", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "busy_code.db")
		blockingPod := openBusyTestDB(t, dbPath)
		newPod := openBusyTestDB(t, dbPath)
		release := lockSQLiteFileHeldByOtherPod(t, blockingPod)
		defer release()

		// Keep the wait short: sqliteDSN sets busy_timeout(10000), and this test
		// wants the driver's SQLITE_BUSY result code, not a 10s wait.
		writeErr := newPod.Exec("PRAGMA busy_timeout = 50").Error
		if writeErr == nil {
			writeErr = insertLockedWrite(newPod, "blocked")
		}
		if writeErr == nil {
			t.Fatalf("expected the second writer to hit the write lock")
		}
		if !IsSQLiteBusyError(writeErr) {
			t.Fatalf("expected a recognized lock error, got %v", writeErr)
		}
	})

	t.Run("wrapped busy message", func(t *testing.T) {
		wrapped := fmt.Errorf("add-bot-from-config[0]: %w", errors.New("database table is locked: keys"))
		if !IsSQLiteBusyError(wrapped) {
			t.Fatalf("wrapped lock message must be recognized")
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if IsSQLiteBusyError(errors.New("no such table: bot_runtime_configs")) {
			t.Fatalf("a schema error must not be treated as a lock error")
		}
	})
}

// TestOpenSQLiteSetsBusyTimeoutAndWAL guards the connection pragmas the startup
// bootstrap relies on: without them a second process fails instantly instead of
// waiting for the write lock.
func TestOpenSQLiteSetsBusyTimeoutAndWAL(t *testing.T) {
	DB, err := openSQLite(filepath.Join(t.TempDir(), "pragma_test.db"))
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}

	var busyTimeout int
	if err := DB.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		t.Fatalf("failed to read busy_timeout: %v", err)
	}
	if busyTimeout != 10000 {
		t.Fatalf("expected busy_timeout 10000, got %d", busyTimeout)
	}

	var journalMode string
	if err := DB.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("failed to read journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("expected journal_mode wal, got %q", journalMode)
	}
}

// TestRetryOnBusyRetriesUntilUnlocked is the overlapping-pod smoke test from
// ci#679: a second writer holds the write lock while the retrying startup write
// runs, and the write must succeed once that writer lets go instead of
// crash-looping the process.
func TestRetryOnBusyRetriesUntilUnlocked(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy.db")
	blockingPod := openBusyTestDB(t, dbPath)
	newPod := openBusyTestDB(t, dbPath)
	releaseLock := lockSQLiteFileHeldByOtherPod(t, blockingPod)

	if err := newPod.Exec("PRAGMA busy_timeout = 50").Error; err != nil {
		t.Fatalf("failed to shorten busy_timeout: %v", err)
	}

	// Release the lock only after the first attempts have already failed.
	released := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		releaseLock()
		close(released)
	}()

	attempts := 0
	policy := BusyRetryPolicy{Attempts: 15, InitialDelay: 20 * time.Millisecond, MaxDelay: 100 * time.Millisecond}
	err := RetryOnBusyWithPolicy(context.Background(), policy, "add-bot-from-config", func() error {
		attempts++
		return insertLockedWrite(newPod, fmt.Sprintf("bootstrap-%d", attempts))
	})
	if err != nil {
		t.Fatalf("expected the startup write to survive lock contention, got %v", err)
	}
	<-released
	if attempts < 2 {
		t.Fatalf("expected the write to be retried while the lock was held, attempts=%d", attempts)
	}

	var rows int64
	if err := newPod.Table("locked_writes").Count(&rows).Error; err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly one committed write, got %d", rows)
	}
}

func TestRetryOnBusyDoesNotRetryPermanentErrors(t *testing.T) {
	attempts := 0
	sentinel := errors.New("no such table: bot_runtime_configs")
	err := RetryOnBusy(context.Background(), "add-bot-from-config", func() error {
		attempts++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the original error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected a single attempt for a permanent error, got %d", attempts)
	}
}

func TestRetryOnBusyExhaustsAttempts(t *testing.T) {
	attempts := 0
	policy := BusyRetryPolicy{Attempts: 3, InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
	err := RetryOnBusyWithPolicy(context.Background(), policy, "add-bot-from-config", func() error {
		attempts++
		return errors.New("database is locked (5) (SQLITE_BUSY)")
	})
	if err == nil {
		t.Fatalf("expected an error once the attempt budget is exhausted")
	}
	if !strings.Contains(err.Error(), "still held after 3 attempts") {
		t.Fatalf("expected the lock to be named in the error, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryOnBusyHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	policy := BusyRetryPolicy{Attempts: 5, InitialDelay: time.Second, MaxDelay: time.Second}

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := RetryOnBusyWithPolicy(ctx, policy, "add-bot-from-config", func() error {
		return errors.New("database is locked (5) (SQLITE_BUSY)")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("expected cancellation to cut the backoff short, waited %s", elapsed)
	}
}
