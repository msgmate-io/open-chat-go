package cmd

import (
	"backend/database"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/bcrypt"
)

func TestResolveRootCredentialsUsesBootstrapAdminEntry(t *testing.T) {
	t.Setenv("ROOT_CREDENTIALS", "")
	spec := `[{"username":"admin","password":"BootstrapPass1!","is_admin":true}]`
	creds, err := resolveRootCredentials(&cli.Command{Name: "test"}, []string{spec})
	if err != nil {
		t.Fatalf("expected bootstrap.users admin entry to satisfy missing ROOT_CREDENTIALS: %v", err)
	}
	if creds != "admin:BootstrapPass1!" {
		t.Fatalf("expected admin credentials from bootstrap.users, got %q", creds)
	}
}

func TestResolveRootCredentialsErrorsWithoutAdminEntry(t *testing.T) {
	t.Setenv("ROOT_CREDENTIALS", "")
	_, err := resolveRootCredentials(&cli.Command{Name: "test"}, []string{`[{"username":"other","password":"pw"}]`})
	if err == nil {
		t.Fatalf("expected error when ROOT_CREDENTIALS is unset and no admin bootstrap entry exists")
	}
	if !strings.Contains(err.Error(), "ROOT_CREDENTIALS") {
		t.Fatalf("expected error to mention ROOT_CREDENTIALS, got %v", err)
	}
}

func TestFindAdminBootstrapUserAdminEntry(t *testing.T) {
	spec := `[{"username":"ci-bot","password":"BotPass1!","is_automated":true},{"username":"admin","password":"BootstrapPass1!"}]`
	cfg, err := findAdminBootstrapUser([]string{spec})
	if err != nil {
		t.Fatalf("findAdminBootstrapUser failed: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected admin entry to be found")
	}
	if cfg.Username != "admin" {
		t.Fatalf("expected admin entry, got %q", cfg.Username)
	}
}

func TestFindAdminBootstrapUserNoEntry(t *testing.T) {
	cfg, err := findAdminBootstrapUser([]string{`{"username":"other","password":"pw"}`})
	if err != nil {
		t.Fatalf("findAdminBootstrapUser failed: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil when no admin entry is declared, got %v", cfg.Username)
	}
}

func TestBootstrapAdminViaUserConfigWhenRootCredentialsUnset(t *testing.T) {
	t.Setenv("ROOT_CREDENTIALS", "")
	DB := database.SetupDatabase(*setupBootstrapUserTestDB(t))

	creds, err := resolveRootCredentials(&cli.Command{Name: "test"}, []string{`{"username":"admin","password":"BootstrapPass1!","is_admin":true}`})
	if err != nil {
		t.Fatalf("resolveRootCredentials failed: %v", err)
	}

	admin, err := ensureBootstrapUser(DB, bootstrapUserSpec{
		Label:            "root-credentials",
		Credentials:      creds,
		IsAdmin:          true,
		SingletonAdmin:   true,
		ValidateStrength: false,
	})
	if err != nil {
		t.Fatalf("ensureBootstrapUser failed: %v", err)
	}

	loginErr := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("BootstrapPass1!"))
	if loginErr != nil {
		t.Fatalf("expected admin password from bootstrap.users entry to authenticate: %v", loginErr)
	}
	if admin == nil || !admin.IsAdmin {
		t.Fatalf("expected admin user with admin flag")
	}
}
