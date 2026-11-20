package database

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupIntegrationAccessDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "integration_access_test.db"),
		ResetDB:  true,
	})
	if db == nil {
		t.Fatal("failed to set up test database")
	}
	return db
}

// A revoke soft-deletes the grant while idx_user_integration_access keeps
// covering (user_id, integration_name). Re-granting must revive that tombstone
// instead of inserting a second row: the insert used to fail with a unique
// violation, which the boot-time EnsureDefaultAdminIntegrationAccess migration
// turned into a crash loop.
func TestEnsureIntegrationAccessRevivesRevokedGrant(t *testing.T) {
	db := setupIntegrationAccessDB(t)

	user := User{Username: "revoke-revoke", Email: "revoke@example.com"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := EnsureIntegrationAccess(db, user.ID, "admin"); err != nil {
		t.Fatalf("initial grant: %v", err)
	}
	if err := RevokeIntegrationAccess(db, user.ID, "admin"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	visible, err := HasIntegrationAccess(db, user.ID, "admin")
	if err != nil {
		t.Fatalf("has access after revoke: %v", err)
	}
	if visible {
		t.Fatal("revoked grant must not be visible")
	}

	if err := EnsureIntegrationAccess(db, user.ID, "admin"); err != nil {
		t.Fatalf("re-grant after revoke: %v", err)
	}

	visible, err = HasIntegrationAccess(db, user.ID, "admin")
	if err != nil {
		t.Fatalf("has access after re-grant: %v", err)
	}
	if !visible {
		t.Fatal("re-granted access must be visible again")
	}

	var total int64
	if err := db.Unscoped().Model(&IntegrationAccess{}).
		Where("user_id = ? AND integration_name = ?", user.ID, "admin").
		Count(&total).Error; err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected the tombstone to be revived in place, got %d rows", total)
	}
}

// Repeated grants (the boot migration runs on every start) must stay idempotent
// and must not duplicate rows.
func TestEnsureIntegrationAccessIsIdempotent(t *testing.T) {
	db := setupIntegrationAccessDB(t)

	user := User{Username: "idempotent-grant", Email: "idempotent@example.com"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := EnsureIntegrationAccess(db, user.ID, "  AdMiN  "); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}

	rows, err := ListIntegrationAccessByUserID(db, user.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(rows))
	}
	if rows[0].IntegrationName != "admin" {
		t.Fatalf("expected normalized name %q, got %q", "admin", rows[0].IntegrationName)
	}
}

// EnsureDefaultAdminIntegrationAccess is the boot migration that crash-looped
// on the tombstone; it has to succeed (and restore the grant) on a database
// where the admin's grant was revoked earlier.
func TestEnsureDefaultAdminIntegrationAccessSurvivesRevokedGrant(t *testing.T) {
	db := setupIntegrationAccessDB(t)

	admin := User{Username: "boot-admin", Email: "boot-admin@example.com", IsAdmin: true}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	// Reproduce the live incident: the admin's grant is a soft-deleted
	// tombstone while idx_user_integration_access still covers the row.
	if err := db.Create(&IntegrationAccess{UserId: admin.ID, IntegrationName: "admin"}).Error; err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	if err := RevokeIntegrationAccess(db, admin.ID, "admin"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if err := EnsureIntegrationAccess(db, admin.ID, "admin"); err != nil {
		t.Fatalf("boot-time admin grant panicked the migration with: %v", err)
	}
}
