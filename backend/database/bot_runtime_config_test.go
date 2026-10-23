package database

import (
	"path/filepath"
	"testing"
	"time"
)

// A legacy/raw-SQL row can store default_shared_config as SQLite TEXT. The
// model must scan it instead of returning "unsupported Scan, storing
// driver.Value type string into type *json.RawMessage", which made the live
// bot-runtime lookup fall back (ci#840).
func TestBotRuntimeConfigScansTextDefaultSharedConfig(t *testing.T) {
	DB := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "bot_runtime_jsonraw_test.db"),
		ResetDB:  true,
	})

	now := time.Now()
	legacy := `{"model":"legacy","tools":["coder_bash"]}`
	if err := DB.Exec(
		`INSERT INTO bot_runtime_configs
			(created_at, updated_at, bot_user_id, owner_user_id, name, description, default_shared_config, is_public, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 42, 1, "legacy-text-config", "legacy", legacy, false, true,
	).Error; err != nil {
		t.Fatalf("failed to insert legacy TEXT config: %v", err)
	}

	var runtime BotRuntimeConfig
	if err := DB.Where("name = ?", "legacy-text-config").First(&runtime).Error; err != nil {
		t.Fatalf("failed to load bot runtime config: %v", err)
	}
	if got := string(runtime.DefaultSharedConfig); got != legacy {
		t.Fatalf("DefaultSharedConfig = %q, want %q", got, legacy)
	}
}
