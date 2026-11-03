package database

import "testing"

// TestVacuumTuningMigrationIsNoOpOnSQLite pins the portability contract: the
// Postgres storage-parameter tuning must not run (or fail) on the SQLite
// backend the project also supports.
func TestVacuumTuningMigrationIsNoOpOnSQLite(t *testing.T) {
	db := setupIndexMigrationTestDB(t)
	if db.Dialector.Name() != "sqlite" {
		t.Fatalf("expected a sqlite test database, got %q", db.Dialector.Name())
	}
	if err := (VacuumTuningMigration{}).Migrate(db); err != nil {
		t.Fatalf("VacuumTuningMigration must be a no-op on SQLite: %v", err)
	}
	// Nil handle must also be safe.
	if err := (VacuumTuningMigration{}).Migrate(nil); err != nil {
		t.Fatalf("VacuumTuningMigration(nil) must be a no-op: %v", err)
	}
}
