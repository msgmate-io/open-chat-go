package cmd

import (
	"context"
	"fmt"

	"backend/database"

	"gorm.io/gorm"
)

// runStartupBootstrapDBStep runs one startup bootstrap step that writes to the
// database, retrying transient SQLite write-lock contention (SQLITE_BUSY /
// "database is locked") with backoff instead of failing the process.
//
// ci#679: on a ReadWriteOnce SQLite volume a rollout can briefly overlap the
// outgoing pod (which still holds the write lock) with the incoming one. That
// made `add-bot-from-config[0]` fail at startup, exit(1), and kill every
// in-flight agent turn. The condition is transient and every bootstrap step is
// an idempotent upsert, so it is safe to wait and re-run the step; only a real
// error (or an exhausted retry budget) still aborts startup.
func runStartupBootstrapDBStep(ctx context.Context, label string, step func() error) error {
	if err := database.RetryOnBusy(ctx, label, step); err != nil {
		if database.IsSQLiteBusyError(err) {
			return fmt.Errorf("%s: the SQLite database stayed locked by another process (overlapping rollout or unreleased write lock); quiesce the previous instance or wait for it to terminate: %w", label, err)
		}
		return err
	}
	return nil
}

// ensureBootstrapUserWithBusyRetry is ensureBootstrapUser guarded by the
// SQLITE_BUSY retry loop (ci#679): bootstrap user creation writes the user,
// contact token and 2FA state, all idempotent because CreateUser returns the
// existing row instead of inserting a duplicate.
func ensureBootstrapUserWithBusyRetry(ctx context.Context, label string, DB *gorm.DB, spec bootstrapUserSpec) (*database.User, error) {
	var user *database.User
	err := runStartupBootstrapDBStep(ctx, label, func() error {
		var stepErr error
		user, stepErr = ensureBootstrapUser(DB, spec)
		return stepErr
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}
