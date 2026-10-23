package database

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupAccountStateFallbackTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "account_state_fallback_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return SetupTestDatabase(t, cfg)
}

func createAccountStatesTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("CREATE TABLE account_states (user_id integer primary key, suspended boolean, is_email_verified boolean)").Error; err != nil {
		t.Fatalf("failed to create account_states table: %v", err)
	}
}

func TestIsUserSuspendedMissingTable(t *testing.T) {
	db := setupAccountStateFallbackTestDB(t)
	suspended, err := IsUserSuspended(db, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suspended {
		t.Fatalf("expected not suspended when account_states table is absent")
	}
}

func TestIsUserSuspended(t *testing.T) {
	db := setupAccountStateFallbackTestDB(t)
	createAccountStatesTable(t, db)

	if err := db.Exec("INSERT INTO account_states (user_id, suspended, is_email_verified) VALUES (?, ?, ?)", 1, true, true).Error; err != nil {
		t.Fatalf("failed to seed suspended state: %v", err)
	}
	if err := db.Exec("INSERT INTO account_states (user_id, suspended, is_email_verified) VALUES (?, ?, ?)", 2, false, true).Error; err != nil {
		t.Fatalf("failed to seed active state: %v", err)
	}

	if suspended, err := IsUserSuspended(db, 1); err != nil || !suspended {
		t.Fatalf("expected user 1 suspended (suspended=%v err=%v)", suspended, err)
	}
	if suspended, err := IsUserSuspended(db, 2); err != nil || suspended {
		t.Fatalf("expected user 2 active (suspended=%v err=%v)", suspended, err)
	}
	if suspended, err := IsUserSuspended(db, 3); err != nil || suspended {
		t.Fatalf("expected missing user active (suspended=%v err=%v)", suspended, err)
	}
	if suspended, err := IsUserSuspended(nil, 1); err != nil || suspended {
		t.Fatalf("expected nil db to report not suspended (suspended=%v err=%v)", suspended, err)
	}
}
