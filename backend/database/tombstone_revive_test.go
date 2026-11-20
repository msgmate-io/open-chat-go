package database

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupTombstoneReviveDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "tombstone_revive_test.db"),
		ResetDB:  true,
	})
	if db == nil {
		t.Fatal("failed to set up test database")
	}
	return db
}

// The boot-time BackfillBotRuntimeOwnersMigration re-ensures one owner row per
// runtime on every start. When a transferred owner's grant was soft-deleted by
// the config bootstrap, that FirstOrCreate collided with
// idx_bot_runtime_owner and panicked the whole migration pass - the ci#1154
// shape on a different table.
func TestEnsureBotRuntimeOwnerRevivesRevokedOwner(t *testing.T) {
	db := setupTombstoneReviveDB(t)

	owner := User{Username: "bot-owner-revive", Email: "bot-owner-revive@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	bot := User{Username: "bot-owner-revive-bot", Email: "bot-owner-revive-bot@example.com"}
	if err := db.Create(&bot).Error; err != nil {
		t.Fatalf("create bot user: %v", err)
	}

	runtime := BotRuntimeConfig{BotUserId: bot.ID, OwnerUserId: owner.ID, Name: "tombstone-owner-revive"}
	if err := db.Create(&runtime).Error; err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	if err := EnsureBotRuntimeOwner(db, runtime.ID, owner.ID); err != nil {
		t.Fatalf("initial grant: %v", err)
	}
	// The config bootstrap revokes a transferred owner with a soft delete.
	if err := db.Where("bot_runtime_config_id = ? AND user_id = ?", runtime.ID, owner.ID).Delete(&BotRuntimeOwner{}).Error; err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if err := EnsureBotRuntimeOwner(db, runtime.ID, owner.ID); err != nil {
		t.Fatalf("re-grant after revoke must revive the tombstone: %v", err)
	}
	if err := (BackfillBotRuntimeOwnersMigration{}).Migrate(db); err != nil {
		t.Fatalf("boot backfill must not panic on a revoked owner: %v", err)
	}

	var total int64
	if err := db.Unscoped().Model(&BotRuntimeOwner{}).
		Where("bot_runtime_config_id = ? AND user_id = ?", runtime.ID, owner.ID).
		Count(&total).Error; err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected the tombstone to be revived in place, got %d rows", total)
	}

	var visible int64
	if err := db.Model(&BotRuntimeOwner{}).
		Where("bot_runtime_config_id = ? AND user_id = ?", runtime.ID, owner.ID).
		Count(&visible).Error; err != nil {
		t.Fatalf("count visible owners: %v", err)
	}
	if visible != 1 {
		t.Fatalf("expected the revived owner to be visible, got %d rows", visible)
	}
}

// Deleting a chat soft-deletes its action-task dismissals (api/chats.Delete)
// while idx_action_task_dismissal still covers (user_id, task_key), so
// dismissing the same task again must revive the tombstone instead of failing
// the request with a unique violation.
func TestEnsureActionTaskDismissalRevivesDeletedDismissal(t *testing.T) {
	db := setupTombstoneReviveDB(t)

	user := User{Username: "dismiss-revive", Email: "dismiss-revive@example.com"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	const taskKey = "chat-uuid:message-uuid"
	if err := EnsureActionTaskDismissal(db, user.ID, 42, taskKey); err != nil {
		t.Fatalf("initial dismissal: %v", err)
	}
	if err := db.Where("user_id = ? AND task_key = ?", user.ID, taskKey).Delete(&ActionTaskDismissal{}).Error; err != nil {
		t.Fatalf("chat delete must soften the dismissal: %v", err)
	}
	if err := EnsureActionTaskDismissal(db, user.ID, 42, taskKey); err != nil {
		t.Fatalf("re-dismiss must revive the tombstone: %v", err)
	}

	var total int64
	if err := db.Unscoped().Model(&ActionTaskDismissal{}).
		Where("user_id = ? AND task_key = ?", user.ID, taskKey).
		Count(&total).Error; err != nil {
		t.Fatalf("count dismissals: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected the tombstone to be revived in place, got %d rows", total)
	}
}

// Removing a product landing page soft-deletes its row while
// idx_product_landing_page_integration_slug still covers
// (integration_id, page_slug), so the boot seed and the settings update path
// must revive the tombstone instead of inserting a second row - the ci#1154
// unique-violation shape on another boot-time seed.
func TestSeedProductLandingPageConfigsRevivesDeletedPage(t *testing.T) {
	db := setupTombstoneReviveDB(t)

	def := ProductLandingPageDefaults[0]
	if err := SeedProductLandingPageConfigs(db); err != nil {
		t.Fatalf("initial seed: %v", err)
	}
	if err := db.Where("integration_id = ? AND page_slug = ?", def.IntegrationId, def.PageSlug).
		Delete(&ProductLandingPageConfig{}).Error; err != nil {
		t.Fatalf("remove page: %v", err)
	}

	if err := SeedProductLandingPageConfigs(db); err != nil {
		t.Fatalf("re-seed must revive the tombstone, not insert: %v", err)
	}
	if err := (SeedProductLandingPageConfigsMigration{}).Migrate(db); err != nil {
		t.Fatalf("boot seed migration must not fail on a removed page: %v", err)
	}

	var total int64
	if err := db.Unscoped().Model(&ProductLandingPageConfig{}).
		Where("integration_id = ? AND page_slug = ?", def.IntegrationId, def.PageSlug).
		Count(&total).Error; err != nil {
		t.Fatalf("count pages: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected the tombstone to be revived in place, got %d rows", total)
	}

	// Operator edits survive the revive.
	if _, err := UpdateProductLandingPageConfig(db, def.IntegrationId, def.PageSlug, ProductLandingPageConfigUpdate{
		UpdatedBy: "ops",
	}); err != nil {
		t.Fatalf("update page: %v", err)
	}
	if err := SeedProductLandingPageConfigs(db); err != nil {
		t.Fatalf("re-seed after edit: %v", err)
	}
	stored, err := GetProductLandingPageConfig(db, def.IntegrationId, def.PageSlug)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if stored.UpdatedBy != "ops" {
		t.Fatalf("seed must not overwrite operator edits, updated_by = %q", stored.UpdatedBy)
	}
}

// BackfillBotRuntimeOwnersMigration runs on every boot, so it has to survive a
// grant that the config bootstrap soft-deleted (ci#1154's crash-loop shape).
func TestBackfillBotRuntimeOwnersMigrationSurvivesRevokedOwner(t *testing.T) {
	db := setupTombstoneReviveDB(t)

	owner := User{Username: "backfill-owner", Email: "backfill-owner@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	bot := User{Username: "backfill-bot", Email: "backfill-bot@example.com"}
	if err := db.Create(&bot).Error; err != nil {
		t.Fatalf("create bot user: %v", err)
	}
	runtime := BotRuntimeConfig{BotUserId: bot.ID, OwnerUserId: owner.ID, Name: "backfill-revoked"}
	if err := db.Create(&runtime).Error; err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := EnsureBotRuntimeOwner(db, runtime.ID, owner.ID); err != nil {
		t.Fatalf("initial grant: %v", err)
	}
	if err := db.Where("bot_runtime_config_id = ? AND user_id = ?", runtime.ID, owner.ID).
		Delete(&BotRuntimeOwner{}).Error; err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if err := (BackfillBotRuntimeOwnersMigration{}).Migrate(db); err != nil {
		t.Fatalf("boot backfill must survive a revoked owner: %v", err)
	}
	if err := (BackfillBotRuntimeOwnersMigration{}).Migrate(db); err != nil {
		t.Fatalf("boot backfill must stay idempotent: %v", err)
	}

	var total int64
	if err := db.Unscoped().Model(&BotRuntimeOwner{}).
		Where("bot_runtime_config_id = ? AND user_id = ?", runtime.ID, owner.ID).
		Count(&total).Error; err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected exactly one revived owner row, got %d", total)
	}
}
