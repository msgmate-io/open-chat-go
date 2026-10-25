package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ci#679: during a rollout the old and the new backend pod briefly share the
// ReadWriteOnce SQLite volume. WAL mode plus busy_timeout (see
// db_sqlite_driver_default.go) and the RetryOnBusy backoff (see sqlite_busy.go)
// make a single write survive that overlap. They do not stop both processes from
// running the same startup seed at the same time, which duplicates work and can
// still collide on unique keys. The bootstrap seed guard below adds the missing
// half of the fix: exactly one process applies a given startup seed, the other
// ones wait for it to finish and then continue.
//
// A guard row is claimed with an atomic `INSERT ... ON CONFLICT DO NOTHING`, so
// only one writer wins even across processes sharing one database file. A
// fingerprint (the seed payload) is stored with the claim: an unchanged payload
// is applied once and never again, a changed payload is re-applied on the next
// rollout (the startup passes are idempotent upserts). Claims of a process that
// died mid-seed expire after BootstrapSeedClaimTTL and are stolen, so a crashed
// pod can never wedge the bootstrap forever.

// BootstrapSeedState is the lifecycle state of a bootstrap seed claim.
type BootstrapSeedState string

const (
	// BootstrapSeedRunning means a process claimed the seed and is applying it.
	BootstrapSeedRunning BootstrapSeedState = "running"
	// BootstrapSeedDone means the seed was applied by the recorded owner.
	BootstrapSeedDone BootstrapSeedState = "done"
)

// BootstrapSeed records that a one-shot startup seed was claimed and applied.
// Only the claiming (single-writer) process writes the seed itself.
type BootstrapSeed struct {
	Name        string    `gorm:"primaryKey;size:255" json:"name"`
	State       string    `gorm:"size:16;not null" json:"state"`
	Fingerprint string    `gorm:"size:64" json:"fingerprint,omitempty"`
	Owner       string    `gorm:"size:255" json:"owner,omitempty"`
	AppliedAt   time.Time `json:"applied_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName keeps the guard table out of the regular model naming scheme.
func (BootstrapSeed) TableName() string {
	return "bootstrap_seeds"
}

var (
	// BootstrapSeedClaimTTL is how long a claim may stay in the running state
	// before another process treats the owner as gone and steals the seed.
	BootstrapSeedClaimTTL = 5 * time.Minute
	// bootstrapSeedPollInterval is how often a waiting process re-checks
	// whether the claiming process finished.
	bootstrapSeedPollInterval = 100 * time.Millisecond
	// bootstrapSeedWaitTimeout bounds how long a waiting process follows
	// another one before it gives up and reports the stall.
	bootstrapSeedWaitTimeout = 5 * time.Minute
)

// BootstrapSeedOwner identifies the process claiming a bootstrap seed. It is
// kept short and human readable because it ends up in logs and error messages.
func BootstrapSeedOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s/%d", host, os.Getpid())
}

// RunBootstrapSeedOnce applies seed at most once per fingerprint across all
// processes sharing the database. It reports whether this call applied the seed
// itself.
//
// Every database access - the guard table migration, the claim, the seed and the
// completion marker - goes through RetryOnBusy, so an overlapping pod holding
// the SQLite write lock makes this wait instead of crash-looping (ci#679).
func RunBootstrapSeedOnce(ctx context.Context, db *gorm.DB, name, fingerprint, owner string, seed func() error) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("bootstrap seed %q: no database handle", name)
	}
	if name == "" {
		return false, fmt.Errorf("bootstrap seed: empty name")
	}
	if seed == nil {
		return false, fmt.Errorf("bootstrap seed %q: no seed function", name)
	}
	if owner == "" {
		owner = BootstrapSeedOwner()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if err := RetryOnBusy(ctx, "bootstrap-seed:"+name+":schema", func() error {
		return ensureBootstrapSeedTable(db)
	}); err != nil {
		return false, err
	}

	deadline := time.Now().Add(bootstrapSeedWaitTimeout)
	for {
		var claimed bool
		if err := RetryOnBusy(ctx, "bootstrap-seed:"+name+":claim", func() error {
			var claimErr error
			claimed, claimErr = claimBootstrapSeed(ctx, db, name, fingerprint, owner)
			return claimErr
		}); err != nil {
			return false, err
		}
		if claimed {
			if err := RetryOnBusy(ctx, "bootstrap-seed:"+name, seed); err != nil {
				// Give the claim up so a later start can retry the seed
				// instead of waiting for the TTL to expire.
				releaseBootstrapSeed(db, name, owner)
				return false, err
			}
			if err := RetryOnBusy(ctx, "bootstrap-seed:"+name+":done", func() error {
				return markBootstrapSeedDone(db, name, owner)
			}); err != nil {
				return true, err
			}
			return true, nil
		}

		var done bool
		var state bootstrapSeedStatus
		if err := RetryOnBusy(ctx, "bootstrap-seed:"+name+":state", func() error {
			var stateErr error
			done, state, stateErr = bootstrapSeedState(ctx, db, name, fingerprint)
			return stateErr
		}); err != nil {
			return false, err
		}
		if done {
			return false, nil
		}
		if !state.exists {
			// The row disappeared between the claim attempt and the read
			// (a failed claim releasing itself): try again.
			continue
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf(
				"bootstrap seed %q is still claimed by %q after %s; it will be stolen once the claim TTL (%s) expires",
				name, state.owner, bootstrapSeedWaitTimeout, BootstrapSeedClaimTTL)
		}

		timer := time.NewTimer(bootstrapSeedPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, fmt.Errorf("bootstrap seed %q: aborted while waiting for %q: %w", name, state.owner, ctx.Err())
		case <-timer.C:
		}
	}
}

// ensureBootstrapSeedTable creates the guard table if it is missing. It is
// deliberately not a plain AutoMigrate call: during a rollout two pods run this
// at the same time, and AutoMigrate's check-then-create sequence loses that race
// ("table `bootstrap_seeds` already exists"). An atomic CREATE TABLE IF NOT
// EXISTS wins it, and the following AutoMigrate only fills in future columns.
func ensureBootstrapSeedTable(db *gorm.DB) error {
	const createTable = `CREATE TABLE IF NOT EXISTS bootstrap_seeds (
		name varchar(255) PRIMARY KEY,
		state varchar(16) NOT NULL,
		fingerprint varchar(64),
		owner varchar(255),
		applied_at timestamp,
		updated_at timestamp
	)`
	if err := db.Exec(createTable).Error; err != nil {
		// A busy lock is transient and belongs to the retry loop; a table that
		// exists is the state we wanted (a second pod created it first).
		if IsSQLiteBusyError(err) || bootstrapSchemaExists(err) {
			if IsSQLiteBusyError(err) {
				return err
			}
			return nil
		}
		return err
	}
	return db.AutoMigrate(&BootstrapSeed{})
}

// bootstrapSchemaExists reports whether err is the "already exists" failure of a
// concurrent CREATE TABLE, which is benign for the guard table: the table we
// wanted is there, and the winning pod's claim row is what the guard reads.
// Drivers phrase the message slightly differently, so it is matched
// case-insensitively.
func bootstrapSchemaExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

// claimBootstrapSeed atomically takes the seed claim. It returns true when this
// process now owns the seed; false means somebody else does (or already did).
func claimBootstrapSeed(ctx context.Context, db *gorm.DB, name, fingerprint, owner string) (bool, error) {
	now := time.Now()
	insert := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&BootstrapSeed{
		Name:        name,
		State:       string(BootstrapSeedRunning),
		Fingerprint: fingerprint,
		Owner:       owner,
		UpdatedAt:   now,
	})
	if insert.Error != nil {
		return false, insert.Error
	}
	if insert.RowsAffected == 1 {
		return true, nil
	}

	// The row exists. Steal it only when the previous owner died mid-seed (a
	// stale running claim) or when a finished seed carries a different payload
	// (a rollout brought new bootstrap config). A live owner is waited for.
	staleBefore := now.Add(-BootstrapSeedClaimTTL)
	steal := db.WithContext(ctx).Model(&BootstrapSeed{}).
		Where("name = ?", name).
		Where(db.Where("state = ? AND updated_at < ?", string(BootstrapSeedRunning), staleBefore).
			Or("state = ? AND fingerprint <> ?", string(BootstrapSeedDone), fingerprint)).
		Updates(map[string]interface{}{
			"state":       string(BootstrapSeedRunning),
			"owner":       owner,
			"fingerprint": fingerprint,
			"updated_at":  now,
		})
	if steal.Error != nil {
		return false, steal.Error
	}
	return steal.RowsAffected == 1, nil
}

// markBootstrapSeedDone records the completed seed for the claiming owner.
func markBootstrapSeedDone(db *gorm.DB, name, owner string) error {
	res := db.Model(&BootstrapSeed{}).
		Where("name = ? AND owner = ?", name, owner).
		Updates(map[string]interface{}{
			"state":      string(BootstrapSeedDone),
			"applied_at": time.Now(),
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("bootstrap seed %q: claim of %q disappeared before it was marked done", name, owner)
	}
	return nil
}

// releaseBootstrapSeed drops a claim after a failed seed so another process can
// pick the work up immediately.
func releaseBootstrapSeed(db *gorm.DB, name, owner string) {
	if err := db.Where("name = ? AND owner = ? AND state = ?", name, owner, string(BootstrapSeedRunning)).
		Delete(&BootstrapSeed{}).Error; err != nil {
		// Not fatal: the claim expires on its own after BootstrapSeedClaimTTL.
		return
	}
}

type bootstrapSeedStatus struct {
	exists  bool
	owner   string
	state   BootstrapSeedState
	applied bool
}

// bootstrapSeedState reports whether the seed is already applied for this
// fingerprint, and who currently holds a running claim.
func bootstrapSeedState(ctx context.Context, db *gorm.DB, name, fingerprint string) (bool, bootstrapSeedStatus, error) {
	var record BootstrapSeed
	err := db.WithContext(ctx).Where("name = ?", name).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, bootstrapSeedStatus{}, nil
	}
	if err != nil {
		return false, bootstrapSeedStatus{}, err
	}

	status := bootstrapSeedStatus{
		exists: true,
		owner:  record.Owner,
		state:  BootstrapSeedState(record.State),
	}
	// A finished seed with the same payload is the idempotent no-op case even
	// if the row still names the previous owner.
	if status.state == BootstrapSeedDone && record.Fingerprint == fingerprint {
		return true, status, nil
	}
	if status.state == BootstrapSeedDone {
		// A different payload: the next claim attempt re-applies it.
		return false, status, nil
	}
	return false, status, nil
}
