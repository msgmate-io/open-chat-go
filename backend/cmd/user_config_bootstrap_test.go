package cmd

import (
	"backend/database"
	"backend/server/util"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func setupUserConfigTestDB(t *testing.T) *database.DBConfig {
	t.Helper()
	return &database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "user_config_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
}

func TestLoadUserBootstrapConfigsFromSpecArray(t *testing.T) {
	spec := `[
		{"username": "admin_two", "password": "StrongPass1!", "is_admin": true},
		{"username": "helper_bot", "password": "StrongPass1!", "is_automated": true}
	]`
	configs, err := loadUserBootstrapConfigsFromSpec(spec)
	if err != nil {
		t.Fatalf("loadUserBootstrapConfigsFromSpec failed: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 user configs, got %d", len(configs))
	}
	if configs[0].Username != "admin_two" || !configs[0].IsAdmin {
		t.Fatalf("unexpected first config: %+v", configs[0])
	}
	if configs[1].Username != "helper_bot" || !configs[1].IsAutomated {
		t.Fatalf("unexpected second config: %+v", configs[1])
	}
}

func TestLoadUserBootstrapConfigsFromSpecSingleObject(t *testing.T) {
	configs, err := loadUserBootstrapConfigsFromSpec(`{"username": "solo", "password": "StrongPass1!"}`)
	if err != nil {
		t.Fatalf("loadUserBootstrapConfigsFromSpec failed: %v", err)
	}
	if len(configs) != 1 || configs[0].Username != "solo" {
		t.Fatalf("unexpected configs: %+v", configs)
	}
}

func TestLoadUserBootstrapConfigsFromSpecEmpty(t *testing.T) {
	configs, err := loadUserBootstrapConfigsFromSpec("   ")
	if err != nil {
		t.Fatalf("loadUserBootstrapConfigsFromSpec failed: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected no configs for empty spec, got %d", len(configs))
	}
}

func TestApplyUserBootstrapConfigFilesCreatesUsers(t *testing.T) {
	DB := database.SetupDatabase(*setupUserConfigTestDB(t))

	spec := `[
		{"username": "bootstrap_admin", "password": "StrongPass1!", "is_admin": true},
		{"username": "bootstrap_bot", "password": "StrongPass1!", "is_automated": true},
		{"username": "bootstrap_user", "password": "StrongPass1!"}
	]`
	if err := applyUserBootstrapConfigFiles(DB, []string{spec}, true); err != nil {
		t.Fatalf("applyUserBootstrapConfigFiles failed: %v", err)
	}

	var admin database.User
	if err := DB.Where("username = ?", "bootstrap_admin").First(&admin).Error; err != nil {
		t.Fatalf("expected bootstrap_admin to exist: %v", err)
	}
	if !admin.IsAdmin {
		t.Fatalf("expected bootstrap_admin to be admin")
	}

	var bot database.User
	if err := DB.Where("username = ?", "bootstrap_bot").First(&bot).Error; err != nil {
		t.Fatalf("expected bootstrap_bot to exist: %v", err)
	}
	if !bot.IsAutomated {
		t.Fatalf("expected bootstrap_bot to be automated")
	}

	var regular database.User
	if err := DB.Where("username = ?", "bootstrap_user").First(&regular).Error; err != nil {
		t.Fatalf("expected bootstrap_user to exist: %v", err)
	}
	if regular.IsAdmin || regular.IsAutomated {
		t.Fatalf("expected bootstrap_user to be a regular user, got admin=%v automated=%v", regular.IsAdmin, regular.IsAutomated)
	}
}

func TestApplyUserBootstrapConfigFilesSetsEmail(t *testing.T) {
	DB := database.SetupDatabase(*setupUserConfigTestDB(t))

	// Simulate a user that already exists (e.g. the singleton admin) whose
	// default email (== username) we want to correct via bootstrap.
	if err, _ := util.CreateUser(DB, "existing_admin", "StrongPass1!", true); err != nil {
		t.Fatalf("failed to create existing_admin: %v", err)
	}

	spec := `[
		{"username": "existing_admin", "password": "StrongPass1!", "email": "admin@example.com", "is_admin": true},
		{"username": "fresh_user", "password": "StrongPass1!", "email": "fresh@example.com"}
	]`
	if err := applyUserBootstrapConfigFiles(DB, []string{spec}, true); err != nil {
		t.Fatalf("applyUserBootstrapConfigFiles failed: %v", err)
	}

	var admin database.User
	if err := DB.Where("username = ?", "existing_admin").First(&admin).Error; err != nil {
		t.Fatalf("expected existing_admin to exist: %v", err)
	}
	if admin.Email != "admin@example.com" {
		t.Fatalf("expected existing_admin email updated to admin@example.com, got %q", admin.Email)
	}

	var fresh database.User
	if err := DB.Where("username = ?", "fresh_user").First(&fresh).Error; err != nil {
		t.Fatalf("expected fresh_user to exist: %v", err)
	}
	if fresh.Email != "fresh@example.com" {
		t.Fatalf("expected fresh_user email fresh@example.com, got %q", fresh.Email)
	}
}

func TestApplyUserBootstrapConfigFilesRejectsMissingFields(t *testing.T) {
	DB := database.SetupDatabase(*setupUserConfigTestDB(t))

	if err := applyUserBootstrapConfigFiles(DB, []string{`[{"password": "StrongPass1!"}]`}, true); err == nil {
		t.Fatalf("expected error for missing username")
	}
	if err := applyUserBootstrapConfigFiles(DB, []string{`[{"username": "no_pass"}]`}, true); err == nil {
		t.Fatalf("expected error for missing password")
	}
}

func TestApplyUserBootstrapConfigFilesPreconfiguresTwoFactor(t *testing.T) {
	DB := database.SetupDatabase(*setupUserConfigTestDB(t))

	spec := `[
		{"username": "mfa_user", "password": "StrongPass1!", "two_factor_secret": "jbswy3dpehpk3pxp"}
	]`
	if err := applyUserBootstrapConfigFiles(DB, []string{spec}, true); err != nil {
		t.Fatalf("applyUserBootstrapConfigFiles failed: %v", err)
	}

	var user database.User
	if err := DB.Where("username = ?", "mfa_user").First(&user).Error; err != nil {
		t.Fatalf("expected mfa_user to exist: %v", err)
	}
	if !user.TwoFactorEnabled {
		t.Fatalf("expected two_factor_enabled to be true")
	}
	if user.TwoFactorSecret != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("expected normalized secret JBSWY3DPEHPK3PXP, got %q", user.TwoFactorSecret)
	}
}

func TestApplyUserBootstrapConfigFilesSeedsRecoveryCodesIdempotently(t *testing.T) {
	DB := database.SetupDatabase(*setupUserConfigTestDB(t))

	spec := `[
		{
			"username": "mfa_recovery_user",
			"password": "StrongPass1!",
			"two_factor_secret": "JBSWY3DPEHPK3PXP",
			"two_factor_recovery_codes": ["11111111", "22222222"]
		}
	]`
	if err := applyUserBootstrapConfigFiles(DB, []string{spec}, true); err != nil {
		t.Fatalf("applyUserBootstrapConfigFiles failed: %v", err)
	}
	// Re-applying must not duplicate the seeded codes.
	if err := applyUserBootstrapConfigFiles(DB, []string{spec}, true); err != nil {
		t.Fatalf("second applyUserBootstrapConfigFiles failed: %v", err)
	}

	var user database.User
	if err := DB.Where("username = ?", "mfa_recovery_user").First(&user).Error; err != nil {
		t.Fatalf("expected mfa_recovery_user to exist: %v", err)
	}

	var codes []database.TwoFactorRecoveryCode
	if err := DB.Where("user_id = ?", user.ID).Find(&codes).Error; err != nil {
		t.Fatalf("failed to load recovery codes: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("expected 2 recovery codes after two applies, got %d", len(codes))
	}

	plaintext := map[string]bool{"11111111": false, "22222222": false}
	for _, code := range codes {
		for candidate := range plaintext {
			if bcrypt.CompareHashAndPassword([]byte(code.CodeHash), []byte(candidate)) == nil {
				plaintext[candidate] = true
			}
		}
	}
	for candidate, matched := range plaintext {
		if !matched {
			t.Fatalf("expected seeded recovery code %s to be stored", candidate)
		}
	}
}

func TestApplyUserBootstrapConfigFilesRejectsInvalidTwoFactorSecret(t *testing.T) {
	DB := database.SetupDatabase(*setupUserConfigTestDB(t))

	spec := `[
		{"username": "bad_mfa_user", "password": "StrongPass1!", "two_factor_secret": "not-base32-1"}
	]`
	err := applyUserBootstrapConfigFiles(DB, []string{spec}, true)
	if err == nil {
		t.Fatalf("expected error for invalid two-factor secret")
	}
	if !strings.Contains(err.Error(), "bad_mfa_user") {
		t.Fatalf("expected error to include the user label, got %q", err.Error())
	}

	var user database.User
	if err := DB.Where("username = ?", "bad_mfa_user").First(&user).Error; err != nil {
		t.Fatalf("expected bad_mfa_user to still be created: %v", err)
	}
	if user.TwoFactorEnabled {
		t.Fatalf("expected two-factor to remain disabled for an invalid secret")
	}
}
