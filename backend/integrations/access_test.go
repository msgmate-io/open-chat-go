package integrations

import (
	"backend/database"
	"path/filepath"
	"testing"

	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

func init() {
	extiface.MustRegister(extiface.Definition{Name: "zztest_public", UserAccessible: true})
	extiface.MustRegister(extiface.Definition{Name: "zztest_grant_gated"})
	extiface.MustRegister(extiface.Definition{Name: "zztest_core_admin_only", AdminOnly: true})
}

func setupAccessTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "integrations_access_gate_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func TestIsVisibleToUserGatesGrants(t *testing.T) {
	DB := setupAccessTestDB(t)

	regular := &database.User{Name: "regular", Username: "regular", Email: "regular@example.com"}
	if err := DB.Create(regular).Error; err != nil {
		t.Fatalf("failed to create regular user: %v", err)
	}
	admin := &database.User{Name: "admin", Username: "admin", Email: "admin@example.com", IsAdmin: true}
	if err := DB.Create(admin).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	publicDef := extiface.Definition{Name: "zztest_public", UserAccessible: true}
	gatedDef := extiface.Definition{Name: "zztest_grant_gated"}
	adminOnlyDef := extiface.Definition{Name: "zztest_core_admin_only", AdminOnly: true}

	// Public catalogue integrations remain visible without a grant.
	if visible, err := IsVisibleToUser(DB, publicDef, regular); err != nil || !visible {
		t.Fatalf("expected public integration visible for regular user (visible=%v err=%v)", visible, err)
	}

	// Grant-gated integrations stay hidden until an explicit grant exists.
	if visible, err := IsVisibleToUser(DB, gatedDef, regular); err != nil || visible {
		t.Fatalf("expected grant-gated integration hidden without grant (visible=%v err=%v)", visible, err)
	}
	if err := database.EnsureIntegrationAccess(DB, regular.ID, gatedDef.Name); err != nil {
		t.Fatalf("failed to grant access: %v", err)
	}
	if visible, err := IsVisibleToUser(DB, gatedDef, regular); err != nil || !visible {
		t.Fatalf("expected grant-gated integration visible after grant (visible=%v err=%v)", visible, err)
	}

	// A grant never unlocks admin-only integrations for regular users.
	if err := database.EnsureIntegrationAccess(DB, regular.ID, adminOnlyDef.Name); err != nil {
		t.Fatalf("failed to grant admin-only access: %v", err)
	}
	if visible, err := IsVisibleToUser(DB, adminOnlyDef, regular); err != nil || visible {
		t.Fatalf("expected admin-only integration hidden for regular user (visible=%v err=%v)", visible, err)
	}

	// Admins see everything, granted or not.
	if visible, err := IsVisibleToUser(DB, gatedDef, admin); err != nil || !visible {
		t.Fatalf("expected gated integration visible for admin (visible=%v err=%v)", visible, err)
	}
	if visible, err := IsVisibleToUser(DB, adminOnlyDef, admin); err != nil || !visible {
		t.Fatalf("expected admin-only integration visible for admin (visible=%v err=%v)", visible, err)
	}

	// Anonymous callers never see anything.
	if visible, err := IsVisibleToUser(DB, publicDef, nil); err != nil || visible {
		t.Fatalf("expected nil user hidden (visible=%v err=%v)", visible, err)
	}
}
