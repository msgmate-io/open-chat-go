package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func createSeedWritesTable(t *testing.T, DB *gorm.DB) {
	t.Helper()
	if err := DB.Exec("CREATE TABLE IF NOT EXISTS seed_writes (id INTEGER PRIMARY KEY, value TEXT)").Error; err != nil {
		t.Fatalf("failed to create seed_writes: %v", err)
	}
}

func insertSeedWrite(DB *gorm.DB, value string) error {
	return DB.Exec("INSERT INTO seed_writes (value) VALUES (?)", value).Error
}

func countSeedWrites(t *testing.T, DB *gorm.DB) int64 {
	t.Helper()
	var rows int64
	if err := DB.Table("seed_writes").Count(&rows).Error; err != nil {
		t.Fatalf("failed to count seed_writes: %v", err)
	}
	return rows
}

// TestRunStartupBootstrapSeedOnceAppliesOnlyOnce is the ci#679 single-writer
// guard test: two backend pods boot on the same SQLite file (the overlapping
// rollout) and run the very same bot-config seed. Exactly one of them may write
// it; the other has to wait for it and continue without duplicating the work.
func TestRunStartupBootstrapSeedOnceAppliesOnlyOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "seed_once.db")
	oldPod := openStartupBusyTestDB(t, dbPath)
	createSeedWritesTable(t, oldPod)
	newPod := openStartupBusyTestDB(t, dbPath)

	var applied int32
	seed := func() error {
		atomic.AddInt32(&applied, 1)
		// Give the second pod time to reach the guard while this one writes.
		time.Sleep(150 * time.Millisecond)
		return insertSeedWrite(newPod, "bot-config-seed")
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = runStartupBootstrapSeedOnce(context.Background(), oldPod, "add-bot-from-config",
			[]string{"bots.yaml"}, seed)
	}()
	go func() {
		defer wg.Done()
		errs[1] = runStartupBootstrapSeedOnce(context.Background(), newPod, "add-bot-from-config",
			[]string{"bots.yaml"}, seed)
	}()
	wg.Wait()

	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent bootstrap seeds must not fail: %v / %v", errs[0], errs[1])
	}
	if got := atomic.LoadInt32(&applied); got != 1 {
		t.Fatalf("expected the seed to be applied exactly once, got %d", got)
	}
	if rows := countSeedWrites(t, newPod); rows != 1 {
		t.Fatalf("expected exactly one seeded row, got %d", rows)
	}

	// A later start with an unchanged payload is a no-op, not a re-run.
	var reran int32
	if err := runStartupBootstrapSeedOnce(context.Background(), newPod, "add-bot-from-config",
		[]string{"bots.yaml"}, func() error {
			atomic.AddInt32(&reran, 1)
			return insertSeedWrite(newPod, "duplicate")
		}); err != nil {
		t.Fatalf("unchanged seed must be a no-op, got %v", err)
	}
	if reran != 0 {
		t.Fatalf("unchanged seed ran again %d time(s)", reran)
	}
	if rows := countSeedWrites(t, newPod); rows != 1 {
		t.Fatalf("expected the row count to stay at 1, got %d", rows)
	}
}

// TestRunStartupBootstrapSeedOnceReappliesOnChangedPayload makes sure the guard
// is an idempotency rule, not a one-shot lockout: a rollout that ships different
// bot config must still be applied.
func TestRunStartupBootstrapSeedOnceReappliesOnChangedPayload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "seed_changed.db")
	DB := openStartupBusyTestDB(t, dbPath)
	createSeedWritesTable(t, DB)

	if err := runStartupBootstrapSeedOnce(context.Background(), DB, "add-bot-from-config",
		[]string{"bots-v1.yaml"}, func() error {
			return insertSeedWrite(DB, "v1")
		}); err != nil {
		t.Fatalf("first seed failed: %v", err)
	}
	if err := runStartupBootstrapSeedOnce(context.Background(), DB, "add-bot-from-config",
		[]string{"bots-v2.yaml"}, func() error {
			return insertSeedWrite(DB, "v2")
		}); err != nil {
		t.Fatalf("seed after a config change failed: %v", err)
	}
	if rows := countSeedWrites(t, DB); rows != 2 {
		t.Fatalf("expected both payload variants to be applied, got %d rows", rows)
	}
}

// TestRunStartupBootstrapSeedOnceSurvivesOverlappingPodLock is the ci#679 crash
// reproduction: the outgoing pod still holds the SQLite write lock while the new
// pod runs the bot-config bootstrap. The bootstrap must retry through the lock
// and finish instead of returning the SQLITE_BUSY error that crash-looped the
// backend.
func TestRunStartupBootstrapSeedOnceSurvivesOverlappingPodLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "seed_locked.db")
	oldPod := openStartupBusyTestDB(t, dbPath)
	createSeedWritesTable(t, oldPod)
	newPod := openStartupBusyTestDB(t, dbPath)
	releaseLock := holdSQLiteWriteLock(t, oldPod)
	shortenBusyTimeout(t, newPod)

	// The outgoing pod keeps the write lock long enough for the incoming pod to
	// hit SQLITE_BUSY at least once, then lets go.
	const lockHold = 400 * time.Millisecond
	lockReleased := make(chan struct{})
	go func() {
		time.Sleep(lockHold)
		releaseLock()
		close(lockReleased)
	}()

	var attempts int32
	start := time.Now()
	err := runStartupBootstrapSeedOnce(context.Background(), newPod, "add-bot-from-config",
		[]string{"bots.yaml"}, func() error {
			atomic.AddInt32(&attempts, 1)
			return insertSeedWrite(newPod, "bot-config-seed")
		})
	elapsed := time.Since(start)
	<-lockReleased

	if err != nil {
		t.Fatalf("expected the bootstrap seed to survive the overlapping lock, got %v", err)
	}
	// The blocked write must have waited for the lock rather than failing fast,
	// i.e. the busy retry path (and not a lucky first attempt) made this pass.
	if elapsed < lockHold/2 {
		t.Fatalf("expected the bootstrap to wait for the held write lock, returned after %s", elapsed)
	}
	// The seed itself must have run once, not once per attempt.
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("expected the seed to be applied exactly once, attempts=%d", got)
	}
	if rows := countSeedWrites(t, newPod); rows != 1 {
		t.Fatalf("expected exactly one committed seed write, got %d", rows)
	}
}

// TestRunStartupBootstrapSeedOncePropagatesRealErrors keeps the guard from
// masking genuine bootstrap defects behind the waiting loop.
func TestRunStartupBootstrapSeedOncePropagatesRealErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "seed_error.db")
	DB := openStartupBusyTestDB(t, dbPath)
	sentinel := errors.New(`bot config "owner_user": owner user must already exist`)

	err := runStartupBootstrapSeedOnce(context.Background(), DB, "add-bot-from-config",
		[]string{"bots.yaml"}, func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the original error, got %v", err)
	}

	// A failed seed releases its claim, so a later start retries it instead of
	// waiting for the claim TTL.
	if err := runStartupBootstrapSeedOnce(context.Background(), DB, "add-bot-from-config",
		[]string{"bots.yaml"}, func() error { return nil }); err != nil {
		t.Fatalf("expected the seed to be retried after a failure, got %v", err)
	}
}

// TestBootstrapSeedFingerprintTracksFileContent pins the payload identity: an
// edited bot-config file must re-run the seed even though the spec path is the
// same string on every restart.
func TestBootstrapSeedFingerprintTracksFileContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bots.yaml")
	if err := os.WriteFile(path, []byte("bots: [v1]\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	first, err := bootstrapSeedFingerprint([]string{path})
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}
	again, err := bootstrapSeedFingerprint([]string{path})
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}
	if first != again {
		t.Fatalf("fingerprint must be stable for unchanged input: %q != %q", first, again)
	}

	if err := os.WriteFile(path, []byte("bots: [v2]\n"), 0o600); err != nil {
		t.Fatalf("failed to rewrite config: %v", err)
	}
	changed, err := bootstrapSeedFingerprint([]string{path})
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}
	if changed == first {
		t.Fatalf("fingerprint must change when the mounted config file changes")
	}

	// A split of the same bytes into different specs must not collide.
	a, err := bootstrapSeedFingerprint([]string{"ab", "c"})
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}
	b, err := bootstrapSeedFingerprint([]string{"a", "bc"})
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}
	if a == b {
		t.Fatalf("different spec splits must not share a fingerprint")
	}
}
