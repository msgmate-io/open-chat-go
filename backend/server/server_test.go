package server

import (
	"backend/database"
	"backend/server/util"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupServerTestDB(t *testing.T) *database.DBConfig {
	t.Helper()
	return &database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "server_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
}

func createAutomatedUser(t *testing.T, DB *gorm.DB, username string, isAdmin bool) *database.User {
	t.Helper()
	err, user := util.CreateUser(DB, username, "Passw0rd!", isAdmin)
	if err != nil {
		t.Fatalf("failed to create user %q: %v", username, err)
	}
	user.IsAutomated = true
	if saveErr := DB.Save(user).Error; saveErr != nil {
		t.Fatalf("failed to set user %q automated: %v", username, saveErr)
	}
	return user
}

func TestSetupBaseConnectionsCreatesDefaultBotRuntimeOwnedByAdmin(t *testing.T) {
	config := setupServerTestDB(t)
	DB := database.SetupDatabase(*config)

	err, admin := util.CreateUser(DB, "admin_user", "AdminPass1!", true)
	if err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}
	botUser := createAutomatedUser(t, DB, "bot_user", false)

	if err := SetupBaseConnections(DB, admin.ID, botUser.ID); err != nil {
		t.Fatalf("SetupBaseConnections failed: %v", err)
	}

	var runtime database.BotRuntimeConfig
	if err := DB.Where("bot_user_id = ?", botUser.ID).First(&runtime).Error; err != nil {
		t.Fatalf("expected runtime config for default bot: %v", err)
	}
	if runtime.OwnerUserId != admin.ID {
		t.Fatalf("expected admin owner_user_id=%d, got %d", admin.ID, runtime.OwnerUserId)
	}
	if runtime.Name == "" {
		t.Fatalf("expected runtime name to be non-empty")
	}
}

func TestSetupBaseConnectionsReassignsDefaultBotRuntimeToAdmin(t *testing.T) {
	config := setupServerTestDB(t)
	DB := database.SetupDatabase(*config)

	err, admin := util.CreateUser(DB, "admin_user", "AdminPass1!", true)
	if err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}
	err, previousOwner := util.CreateUser(DB, "other_owner", "OwnerPass1!", false)
	if err != nil {
		t.Fatalf("failed to create previous owner user: %v", err)
	}
	botUser := createAutomatedUser(t, DB, "bot_user", false)

	runtime := database.BotRuntimeConfig{
		BotUserId:           botUser.ID,
		OwnerUserId:         previousOwner.ID,
		Name:                "bot_user",
		DefaultSharedConfig: []byte(`{"model":"legacy"}`),
		IsPublic:            false,
		IsActive:            true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create initial runtime config: %v", err)
	}

	if err := SetupBaseConnections(DB, admin.ID, botUser.ID); err != nil {
		t.Fatalf("SetupBaseConnections failed: %v", err)
	}

	var updated database.BotRuntimeConfig
	if err := DB.Where("bot_user_id = ?", botUser.ID).First(&updated).Error; err != nil {
		t.Fatalf("expected runtime config for default bot: %v", err)
	}
	if updated.OwnerUserId != admin.ID {
		t.Fatalf("expected reassigned owner_user_id=%d, got %d", admin.ID, updated.OwnerUserId)
	}
}
