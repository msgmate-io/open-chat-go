package cmd

import (
	"backend/database"
	"backend/server/util"
	"path/filepath"
	"testing"
)

func setupBootstrapUserTestDB(t *testing.T) *database.DBConfig {
	t.Helper()
	return &database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "bootstrap_user_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
}

func TestEnsureBootstrapUserSingletonAdminRenamesConflictingUsername(t *testing.T) {
	DB := database.SetupDatabase(*setupBootstrapUserTestDB(t))

	if err, _ := util.CreateUser(DB, "admin", "LegacyPass1!", false); err != nil {
		t.Fatalf("failed to create conflicting user: %v", err)
	}

	admin, err := ensureBootstrapUser(DB, bootstrapUserSpec{
		Label:            "root-credentials",
		Credentials:      "legacyadmin:StrongPass1!",
		IsAdmin:          true,
		SingletonAdmin:   true,
		ValidateStrength: false,
	})
	if err != nil {
		t.Fatalf("ensureBootstrapUser failed: %v", err)
	}
	if admin == nil {
		t.Fatalf("expected admin user")
	}
	if admin.Username != "admin" {
		t.Fatalf("expected singleton admin username to be 'admin', got %q", admin.Username)
	}

	var usersWithAdminUsername int64
	if err := DB.Model(&database.User{}).Where("username = ?", "admin").Count(&usersWithAdminUsername).Error; err != nil {
		t.Fatalf("failed to count admin usernames: %v", err)
	}
	if usersWithAdminUsername != 1 {
		t.Fatalf("expected exactly one user with username 'admin', got %d", usersWithAdminUsername)
	}

	var renamedLegacy database.User
	if err := DB.Where("is_admin = ?", false).First(&renamedLegacy).Error; err != nil {
		t.Fatalf("failed to load renamed legacy user: %v", err)
	}
	if renamedLegacy.Username == "admin" {
		t.Fatalf("expected conflicting legacy username to be auto-renamed")
	}
}
