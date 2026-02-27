package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// SQLite grants a single write lock for the whole database file. Whenever two
// processes share one file - e.g. a new pod starting while the previous pod is
// still shutting down on a ReadWriteOnce volume (ci#679) - a write can fail
// with SQLITE_BUSY ("database is locked") even though the connection sets a
// busy_timeout, because the other process may hold the lock for longer than
// that timeout. The condition is transient by construction and must never abort
// startup, so the startup writers retry it with backoff.
const (
	sqliteBusyResultCode   = 5 // SQLITE_BUSY
	sqliteLockedResultCode = 6 // SQLITE_LOCKED
)

// sqliteResultCoder is implemented by the error types of both SQLite drivers in
// use (github.com/glebarez/go-sqlite, and mattn/go-sqlite3 for the mobile
// build): each exposes the low-level SQLite result code. Relying on this
// interface instead of importing a driver keeps the helper available on every
// build target.
type sqliteResultCoder interface {
	Code() int
}

// IsSQLiteBusyError reports whether err (or any error it wraps) is a transient
// SQLite lock contention failure rather than a real defect.
func IsSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}

	var coder sqliteResultCoder
	if errors.As(err, &coder) {
		switch coder.Code() & 0xff {
		case sqliteBusyResultCode, sqliteLockedResultCode:
			return true
		}
	}

	// Fallback for errors that were re-wrapped by GORM or lost their code.
	message := strings.ToLower(err.Error())
	for _, needle := range []string{
		"database is locked",
		"database table is locked",
		"database schema is locked",
		"sqlite_busy",
		"sqlite_locked",
	} {
		if strings.Contains(message, needle) {
			return true
		}
	}

	return false
}

// BusyRetryPolicy bounds how long RetryOnBusy keeps re-attempting a locked
// operation. The default budget is deliberately generous: an overlapping pod
// holds the write lock only while it terminates (seconds), and every attempt
// additionally waits for the connection's busy_timeout.
type BusyRetryPolicy struct {
	Attempts     int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

// DefaultBusyRetryPolicy is used by RetryOnBusy. With the SQLite
// busy_timeout(10000) set by openSQLite, 20 attempts with a 200ms to 5s backoff
// tolerate well over two minutes of foreign write lock.
var DefaultBusyRetryPolicy = BusyRetryPolicy{
	Attempts:     20,
	InitialDelay: 200 * time.Millisecond,
	MaxDelay:     5 * time.Second,
}

func (p BusyRetryPolicy) normalize() BusyRetryPolicy {
	if p.Attempts < 1 {
		p.Attempts = DefaultBusyRetryPolicy.Attempts
	}
	if p.InitialDelay <= 0 {
		p.InitialDelay = DefaultBusyRetryPolicy.InitialDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = DefaultBusyRetryPolicy.MaxDelay
	}
	return p
}

// RetryOnBusy runs operation, retrying it with exponential backoff while it
// fails with a transient SQLite lock error. Any other error is returned
// immediately, as is the lock error once the attempt budget or ctx is
// exhausted. The operation has to be idempotent; the startup bootstrap writes
// this guards are upserts by design.
func RetryOnBusy(ctx context.Context, label string, operation func() error) error {
	return RetryOnBusyWithPolicy(ctx, DefaultBusyRetryPolicy, label, operation)
}

// RetryOnBusyWithPolicy is RetryOnBusy with an explicit policy (used by tests).
func RetryOnBusyWithPolicy(ctx context.Context, policy BusyRetryPolicy, label string, operation func() error) error {
	policy = policy.normalize()

	delay := policy.InitialDelay
	var err error
	for attempt := 1; attempt <= policy.Attempts; attempt++ {
		err = operation()
		if err == nil {
			if attempt > 1 {
				log.Printf("%s: succeeded after %d attempts", label, attempt)
			}
			return nil
		}
		if !IsSQLiteBusyError(err) {
			return err
		}
		if attempt == policy.Attempts {
			break
		}

		log.Printf("%s: SQLite database is locked by another process (attempt %d/%d), retrying in %s: %v", label, attempt, policy.Attempts, delay, err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%s: aborted while waiting for the SQLite write lock: %w", label, ctx.Err())
		case <-timer.C:
		}
		delay *= 2
		if delay > policy.MaxDelay {
			delay = policy.MaxDelay
		}
	}

	return fmt.Errorf("%s: SQLite write lock still held after %d attempts: %w", label, policy.Attempts, err)
}
