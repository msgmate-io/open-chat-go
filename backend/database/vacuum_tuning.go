package database

import (
	"fmt"

	"backend/logging"

	"gorm.io/gorm"
)

// VacuumTuningMigration applies Postgres per-table autovacuum storage
// parameters to the write-hot tables.
//
// The `messages.meta_data` column is rewritten constantly (queued markers, the
// confirmable-action / runtime-selector updates, the meta refreshes), and the
// default autovacuum scale factor of 0.2 lets dead tuples pile up to ~15% of a
// million-row table before they are reclaimed — bloating the heap, misleading
// the planner, and adding read amplification on top of the index fix
// (IndexMigration). A much lower scale factor keeps the hot tables vacuumed and
// analysed so the planner stays accurate.
//
// It is a no-op on non-Postgres backends (the project also runs SQLite), where
// these storage parameters do not exist, so the migration stays portable.
type VacuumTuningMigration struct{}

type vacuumTuningStatement struct {
	Table string
	SQL   string
	Why   string
}

var vacuumTuningStatements = []vacuumTuningStatement{
	{
		Table: "messages",
		SQL: "ALTER TABLE messages SET (" +
			"autovacuum_vacuum_scale_factor = 0.02, autovacuum_vacuum_threshold = 500, " +
			"autovacuum_analyze_scale_factor = 0.01, autovacuum_analyze_threshold = 500)",
		Why: "constant meta_data churn: keep dead tuples low so the planner and reads stay fast",
	},
	{
		Table: "streaming_messages",
		SQL: "ALTER TABLE streaming_messages SET (" +
			"autovacuum_vacuum_scale_factor = 0.1, autovacuum_analyze_scale_factor = 0.05)",
		Why: "constant streaming-snapshot rewrites",
	},
}

// Migrate applies the tuning. Missing tables are skipped so the migration stays
// usable on partially migrated databases.
func (VacuumTuningMigration) Migrate(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	for _, stmt := range vacuumTuningStatements {
		if !db.Migrator().HasTable(stmt.Table) {
			logging.Debugf("Skipping autovacuum tuning for %s: table does not exist", stmt.Table)
			continue
		}
		logging.Infof("Tuning autovacuum on %s (%s)", stmt.Table, stmt.Why)
		if err := db.Exec(stmt.SQL).Error; err != nil {
			return fmt.Errorf("failed to tune autovacuum on %s: %w", stmt.Table, err)
		}
	}
	return nil
}
