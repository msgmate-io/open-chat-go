package database

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupPermissionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "permission_test.db"),
		ResetDB:  true,
	})
	if db == nil {
		t.Fatal("failed to set up test database")
	}
	return db
}

// Revoking a permission soft-deletes the row while idx_user_permission keeps
// covering (user_id, permission). GrantDefaultPermissionsMigration regrants
// every user's permissions on each boot, so a plain FirstOrCreate + insert
// would crash the boot migration pass with a unique violation. Granting has to
// revive the tombstone.
func TestEnsurePermissionRevivesSoftDeletedGrant(t *testing.T) {
	db := setupPermissionDB(t)

	user := User{Username: "permission-owner", Email: "permission@example.com"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// The user row already carries both default permissions (AfterCreate).
	if err := db.Where("user_id = ? AND permission = ?", user.ID, PermissionCreateBots).Delete(&Permission{}).Error; err != nil {
		t.Fatalf("soft-delete permission: %v", err)
	}

	if err := EnsurePermission(db, user.ID, PermissionCreateBots); err != nil {
		t.Fatalf("re-grant after soft delete: %v", err)
	}

	var granted int64
	if err := db.Model(&Permission{}).
		Where("user_id = ? AND permission = ?", user.ID, PermissionCreateBots).
		Count(&granted).Error; err != nil {
		t.Fatalf("count permissions: %v", err)
	}
	if granted != 1 {
		t.Fatalf("expected the revived permission to be visible, got %d", granted)
	}

	var total int64
	if err := db.Unscoped().Model(&Permission{}).
		Where("user_id = ? AND permission = ?", user.ID, PermissionCreateBots).
		Count(&total).Error; err != nil {
		t.Fatalf("count permissions including deleted: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected the tombstone to be revived in place, got %d rows", total)
	}
}

// GrantDefaultPermissionsMigration runs on every boot, so it must be idempotent
// on a database that already carries the grants.
func TestGrantDefaultPermissionsMigrationIsRepeatable(t *testing.T) {
	db := setupPermissionDB(t)

	user := User{Username: "migration-user", Email: "migration@example.com"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := (GrantDefaultPermissionsMigration{}).Migrate(db); err != nil {
			t.Fatalf("migration run %d: %v", i, err)
		}
	}

	var total int64
	if err := db.Model(&Permission{}).Where("user_id = ?", user.ID).Count(&total).Error; err != nil {
		t.Fatalf("count permissions: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 default permissions for the user, got %d", total)
	}
}
