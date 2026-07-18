package database

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCheckpointWALTruncatesWriteAheadLog guards the ci#850 WAL maintenance: a
// burst of writes grows the -wal file, and a truncating checkpoint returns that
// space to the filesystem instead of leaving the WAL large until an automatic
// checkpoint eventually fires.
func TestCheckpointWALTruncatesWriteAheadLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal_checkpoint.db")
	DB, err := openSQLite(path)
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	defer CloseTestDB(t, DB)

	if err := DB.Exec("CREATE TABLE wal_writes (id INTEGER PRIMARY KEY, value TEXT)").Error; err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	// Keep the automatic checkpoint from firing mid-test so the manual one is
	// what truncates the WAL.
	if err := DB.Exec("PRAGMA wal_autocheckpoint = 0").Error; err != nil {
		t.Fatalf("failed to disable autocheckpoint: %v", err)
	}

	// Enough writes to grow the WAL well past a single page.
	for i := 0; i < 500; i++ {
		if err := DB.Exec("INSERT INTO wal_writes (value) VALUES (?)", "abcdefghijklmnopqrstuvwxyz").Error; err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	walPath := path + "-wal"
	before, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("expected a -wal file to exist: %v", err)
	}
	if before.Size() == 0 {
		t.Fatalf("expected the WAL to have grown, got 0 bytes")
	}

	if err := CheckpointWAL(DB); err != nil {
		t.Fatalf("CheckpointWAL failed: %v", err)
	}

	after, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("expected the -wal file to still exist: %v", err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("expected the truncating checkpoint to shrink the WAL, before=%d after=%d", before.Size(), after.Size())
	}
}

// TestStartWALCheckpointerIsSingleton guards that repeated database setups do
// not spawn a second checkpoint ticker (the once-only contract the process-wide
// SetupDatabase relies on).
func TestStartWALCheckpointerIsSingleton(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal_singleton.db")
	DB, err := openSQLite(path)
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	defer CloseTestDB(t, DB)

	StartWALCheckpointer(DB, time.Hour)
	StartWALCheckpointer(DB, time.Hour)
	StartWALCheckpointer(DB, 0) // ignored

	walCheckpointerMu.Lock()
	running := walCheckpointerRunning
	stop := walCheckpointerCancel
	walCheckpointerMu.Unlock()
	if !running || stop == nil {
		t.Fatalf("expected the checkpointer to be running")
	}

	StopWALCheckpointer()
	walCheckpointerMu.Lock()
	running = walCheckpointerRunning
	walCheckpointerMu.Unlock()
	if running {
		t.Fatalf("expected the checkpointer to be stopped")
	}
}
