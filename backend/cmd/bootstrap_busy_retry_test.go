package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// openStartupBusyTestDB opens another process' view of the same SQLite file.
func openStartupBusyTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	return database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: path,
		ResetDB:  false,
	})
}

// holdSQLiteWriteLock mimics the outgoing pod of ci#679: it holds the SQLite
// write lock on the shared RWO volume while the new process boots. The returned
// function releases the lock.
func holdSQLiteWriteLock(t *testing.T, holder *gorm.DB) func() {
	t.Helper()
	if err := holder.Exec("CREATE TABLE IF NOT EXISTS startup_writes (id INTEGER PRIMARY KEY, value TEXT)").Error; err != nil {
		t.Fatalf("failed to create startup_writes: %v", err)
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

// shortenBusyTimeout pins the booting process to a single connection with a
// short busy_timeout, so a locked write fails quickly instead of blocking for
// the production 10s and the test stays fast.
func shortenBusyTimeout(t *testing.T, DB *gorm.DB) {
	t.Helper()
	pool, err := DB.DB()
	if err != nil {
		t.Fatalf("failed to unwrap sql.DB: %v", err)
	}
	pool.SetMaxOpenConns(1)
	if err := DB.Exec("PRAGMA busy_timeout = 50").Error; err != nil {
		t.Fatalf("failed to shorten busy_timeout: %v", err)
	}
}

func insertStartupWrite(DB *gorm.DB, value string) error {
	return DB.Exec("INSERT INTO startup_writes (value) VALUES (?)", value).Error
}

// TestStartupBootstrapStepSurvivesOverlappingPodLock is the ci#679 startup
// smoke test: with a second process still holding the SQLite write lock, the
// bootstrap step must retry and complete instead of returning a fatal error and
// crash-looping the backend.
func TestStartupBootstrapStepSurvivesOverlappingPodLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "startup_busy.db")
	oldPod := openStartupBusyTestDB(t, dbPath)
	newPod := openStartupBusyTestDB(t, dbPath)
	releaseLock := holdSQLiteWriteLock(t, oldPod)
	shortenBusyTimeout(t, newPod)

	// The previous pod only terminates after the new one already failed once.
	released := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		releaseLock()
		close(released)
	}()

	attempts := 0
	err := runStartupBootstrapDBStep(context.Background(), "add-bot-from-config", func() error {
		attempts++
		return insertStartupWrite(newPod, fmt.Sprintf("write-%d", attempts))
	})
	if err != nil {
		t.Fatalf("expected the startup bootstrap step to survive the overlapping lock, got %v", err)
	}
	<-released
	if attempts < 2 {
		t.Fatalf("expected the step to be retried while the lock was held, attempts=%d", attempts)
	}

	var rows int64
	if err := newPod.Table("startup_writes").Count(&rows).Error; err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly one committed bootstrap write, got %d", rows)
	}
}

// TestStartupBootstrapStepSurfacesNonLockErrors makes sure the retry loop does
// not swallow real startup defects.
func TestStartupBootstrapStepSurfacesNonLockErrors(t *testing.T) {
	err := runStartupBootstrapDBStep(context.Background(), "add-bot-from-config", func() error {
		return fmt.Errorf("bot config %q: owner user must already exist: %w", "owner_user", gorm.ErrRecordNotFound)
	})
	if err == nil {
		t.Fatalf("expected a non-lock error to abort startup")
	}
	if !strings.Contains(err.Error(), "owner user must already exist") {
		t.Fatalf("expected the original error to be preserved, got %v", err)
	}
}

// TestEnsureBootstrapUserWithBusyRetryCreatesUser exercises the wrapped user
// bootstrap path against a real lock: CreateUser is the writer that produced the
// ci#679 `add-bot-from-config[0]` failure, and it must surface the lock as a
// retryable error instead of exiting the process.
func TestEnsureBootstrapUserWithBusyRetryCreatesUser(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "startup_users.db")
	oldPod := openStartupBusyTestDB(t, dbPath)
	newPod := openStartupBusyTestDB(t, dbPath)
	releaseLock := holdSQLiteWriteLock(t, oldPod)
	shortenBusyTimeout(t, newPod)

	released := make(chan struct{})
	go func() {
		time.Sleep(250 * time.Millisecond)
		releaseLock()
		close(released)
	}()

	user, err := ensureBootstrapUserWithBusyRetry(context.Background(), "add-bot-from-config", newPod, bootstrapUserSpec{
		Label:       "add-bot-from-config[0]",
		Credentials: "rollout_bot:RolloutPass1!",
		IsAutomated: true,
	})
	<-released
	if err != nil {
		t.Fatalf("expected the bootstrap user to be created despite the lock, got %v", err)
	}
	if user == nil || user.Username != "rollout_bot" {
		t.Fatalf("expected the rollout_bot user, got %#v", user)
	}

	existing, err := findUserByUsername(newPod, "rollout_bot")
	if err != nil {
		t.Fatalf("expected the bootstrap user to be persisted: %v", err)
	}
	if !existing.IsAutomated {
		t.Fatalf("expected the bootstrap user to be marked automated")
	}
}
