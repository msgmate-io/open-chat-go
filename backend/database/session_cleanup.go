package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Expired session rows are never removed on their own: the auth middleware only
// rejects an expired token (see server.resolveValidSessionFromRequest), a logout
// deletes the sessions it was asked to delete and an automated turn deletes its
// own session when it finishes. Everything else - the 24h browser sessions of
// `Login`, abandoned bot sessions whose turn died before the deferred cleanup ran
// - stays in the table forever. On the single-file SQLite volume, which the
// deployment notes flag as a growth risk, that is pure ballast: an ever-growing
// table that no read path can ever need again.
//
// The sweeper closes that hole (ci#729 follow-up): it deletes sessions that are
// expired *and* past a grace window, on an interval, from the server process. The
// expiry column is indexed, so the delete is a range scan and cheap.
const (
	// DefaultSessionSweepInterval is how often the server sweeps expired
	// sessions when OPEN_CHAT_SESSION_SWEEP_INTERVAL is not set.
	DefaultSessionSweepInterval = time.Hour

	// ExpandedSessionGrace is the margin kept beyond a session's expiry before
	// the sweeper deletes it. It is not needed for correctness (an expired token
	// is rejected anyway) but it keeps a row around long enough that a clock
	// skew between the process that wrote the row and the process sweeping it,
	// or a refresh that lands a moment late, cannot race the delete.
	ExpiredSessionGrace = time.Hour

	// SessionSweepIntervalEnv overrides the sweep interval (a Go duration, or
	// `off` / `disabled` / `never` / `0` to keep the sweeper disabled).
	SessionSweepIntervalEnv = "OPEN_CHAT_SESSION_SWEEP_INTERVAL"
)

// SessionSweepInterval resolves how often the expired-session sweeper runs. An
// unset or unparsable value falls back to the default; an explicit
// `off`/`disabled`/`never`/`0` (or any non-positive duration) disables it.
func SessionSweepInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(SessionSweepIntervalEnv))
	if raw == "" {
		return DefaultSessionSweepInterval
	}

	switch strings.ToLower(raw) {
	case "off", "disabled", "never", "false", "no":
		return 0
	}

	interval, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("%s=%q is not a duration; using the default %s", SessionSweepIntervalEnv, raw, DefaultSessionSweepInterval)
		return DefaultSessionSweepInterval
	}
	if interval <= 0 {
		return 0
	}
	return interval
}

// DeleteExpiredSessions removes every session that expired before
// now-ExpiredSessionGrace and reports how many rows it deleted. It is idempotent
// and safe to run from several processes at once: a row that another sweeper
// already deleted is simply not counted. Transient SQLite write-lock errors are
// retried (the sweep can overlap a bot turn's writes) and anything else is
// returned to the caller.
//
// The delete is unscoped on purpose. `Session` embeds gorm.Model, so a plain
// Delete would only stamp deleted_at and leave the row (and its bytes) in the
// table - which is exactly the growth this sweeper exists to stop. Soft-deleted
// sessions are also invisible to every read path anyway (the auth middleware and
// the admin counts both go through the default scope), so keeping them has no
// value.
func DeleteExpiredSessions(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, errors.New("database: a db handle is required to sweep expired sessions")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		// RetryOnBusy only observes ctx while it is waiting for a retry, so a
		// cancelled context has to be checked before the first write: during
		// shutdown the sweep must not start new work.
		return 0, fmt.Errorf("database: aborted before sweeping expired sessions: %w", err)
	}
	if now.IsZero() {
		now = time.Now()
	}

	cutoff := now.Add(-ExpiredSessionGrace)
	var deleted int64
	err := RetryOnBusy(ctx, "delete-expired-sessions", func() error {
		result := db.Unscoped().Where("expiry < ?", cutoff).Delete(&Session{})
		if result.Error != nil {
			return result.Error
		}
		deleted = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// sweepSessionsOnce runs one sweep and logs only when it removed something (or
// failed), so a quiet server stays quiet.
func sweepSessionsOnce(ctx context.Context, db *gorm.DB) {
	deleted, err := DeleteExpiredSessions(ctx, db, time.Now())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		log.Printf("expired session sweep failed: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("expired session sweep removed %d session(s)", deleted)
	}
}

// RunSessionSweeper sweeps expired sessions once at startup and then every
// interval until ctx is done.
func RunSessionSweeper(ctx context.Context, db *gorm.DB, interval time.Duration) {
	if db == nil || interval <= 0 {
		return
	}

	sweepSessionsOnce(ctx, db)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepSessionsOnce(ctx, db)
		}
	}
}

// StartSessionSweeper keeps the expired-session sweep running in the background
// and returns the stop function the caller must defer (it waits for the
// goroutine to finish, so no sweep can outlive the server). A non-positive
// interval disables the sweeper entirely.
func StartSessionSweeper(db *gorm.DB, interval time.Duration) func() {
	if db == nil || interval <= 0 {
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSessionSweeper(ctx, db, interval)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}
