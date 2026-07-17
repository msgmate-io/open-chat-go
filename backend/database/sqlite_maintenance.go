package database

import (
	"context"
	"sync"
	"time"

	"backend/logging"

	"gorm.io/gorm"
)

// defaultWALCheckpointInterval bounds how long the SQLite write-ahead log may
// grow between explicit truncating checkpoints. The connection already sets
// `wal_autocheckpoint(1000)`, which checkpoints automatically once the WAL
// reaches ~4MB; this periodic truncating checkpoint additionally returns the
// space to the filesystem and keeps the WAL from lingering after a burst of
// writes (ci#850).
const defaultWALCheckpointInterval = 60 * time.Second

var (
	walCheckpointerMu      sync.Mutex
	walCheckpointerCancel  context.CancelFunc
	walCheckpointerDone    chan struct{}
	walCheckpointerRunning bool
)

// CheckpointWAL runs a truncating WAL checkpoint and returns any error. It is
// exported so callers (and tests) can force a checkpoint without waiting for
// the background ticker.
func CheckpointWAL(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error
}

// StartWALCheckpointer starts a single background goroutine that periodically
// truncates the SQLite WAL. It is a no-op when interval is not positive and it
// only ever starts one goroutine per process, so repeated database setups do
// not spawn duplicate tickers. Stop it with StopWALCheckpointer (the ci#811
// teardown contract: the goroutine is drained by its own stop func).
func StartWALCheckpointer(db *gorm.DB, interval time.Duration) {
	if db == nil || interval <= 0 {
		return
	}
	walCheckpointerMu.Lock()
	defer walCheckpointerMu.Unlock()
	if walCheckpointerRunning {
		return
	}
	walCheckpointerRunning = true

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	walCheckpointerCancel = cancel
	walCheckpointerDone = done

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := CheckpointWAL(db); err != nil {
					logging.Debugf("SQLite WAL checkpoint failed: %v", err)
				}
			}
		}
	}()
}

// StopWALCheckpointer stops the background checkpointer and waits for its
// goroutine to exit. It is safe to call when no checkpointer is running.
func StopWALCheckpointer() {
	walCheckpointerMu.Lock()
	cancel := walCheckpointerCancel
	done := walCheckpointerDone
	walCheckpointerCancel = nil
	walCheckpointerDone = nil
	walCheckpointerRunning = false
	walCheckpointerMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
