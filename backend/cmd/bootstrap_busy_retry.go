package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"

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

// runStartupBootstrapSeedOnce runs one startup seed (the bot-config bootstrap)
// exactly once per payload fingerprint across every process that shares the
// database, on top of the SQLITE_BUSY retry loop.
//
// ci#679: two overlapping pods must not both write the seed. During a rollout
// the outgoing pod still holds the SQLite write lock and the incoming pod boots
// on the same ReadWriteOnce volume; without a guard both pods would run the
// same seed concurrently, duplicate the work and collide on unique keys. The
// guard in database.RunBootstrapSeedOnce elects a single writer, makes the
// waiting pod follow it instead of racing, and records the applied fingerprint
// so a later start skips the seed entirely. A changed payload (new bootstrap
// config in the rollout) re-applies it, and the seed itself stays an upsert.
func runStartupBootstrapSeedOnce(ctx context.Context, DB *gorm.DB, name string, payload interface{}, step func() error) error {
	fingerprint, err := bootstrapSeedFingerprint(payload)
	if err != nil {
		return fmt.Errorf("%s: failed to fingerprint the bootstrap payload: %w", name, err)
	}
	_, err = database.RunBootstrapSeedOnce(ctx, DB, name, fingerprint, database.BootstrapSeedOwner(), func() error {
		return runStartupBootstrapDBStep(ctx, name, step)
	})
	return err
}

// bootstrapSeedFingerprint is the stable identity of a bootstrap payload: any
// change in the configured specs changes the fingerprint and re-runs the seed,
// an unchanged configuration is a no-op after the first successful start.
//
// String payloads are treated as config *sources* (a path, URL or inline spec):
// for a readable local file the file content is hashed as well, so editing a
// mounted bot-config file still takes effect on the next restart even though the
// spec string itself did not change.
func bootstrapSeedFingerprint(payload interface{}) (string, error) {
	digest := sha256.New()
	if specs, ok := payload.([]string); ok {
		for _, spec := range specs {
			writeFingerprintPart(digest, spec)
			if info, err := os.Stat(spec); err == nil && info.Mode().IsRegular() {
				content, readErr := os.ReadFile(spec)
				if readErr != nil {
					return "", readErr
				}
				writeFingerprintPart(digest, string(content))
			}
		}
		return hex.EncodeToString(digest.Sum(nil)), nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	writeFingerprintPart(digest, string(encoded))
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// writeFingerprintPart feeds one length-prefixed part into the digest so that
// concatenated values cannot collide with a different split of the same bytes.
func writeFingerprintPart(digest hash.Hash, part string) {
	fmt.Fprintf(digest, "%d:%s\n", len(part), part)
}
