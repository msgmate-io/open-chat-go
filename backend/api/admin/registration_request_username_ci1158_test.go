package admin

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

var registrationUsernameModelsOnce sync.Once

// ensureRegistrationRequestModelRegistered mirrors the production wiring: the
// RegistrationRequest table is registered as an external model by the server
// bootstrap, so a test DB has to register it before SetupTestDatabase runs the
// migrations.
func ensureRegistrationRequestModelRegistered() {
	registrationUsernameModelsOnce.Do(func() {
		database.RegisterExternalModels(&database.RegistrationRequest{})
	})
}

func setupRegistrationUsernameTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	ensureRegistrationRequestModelRegistered()
	return database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "registration_request_username_test.db"),
		Debug:    false,
		ResetDB:  true,
	})
}

// approveRegistrationRequestForTest drives ApproveRegistrationRequest for the
// given request UUID as the supplied admin user.
func approveRegistrationRequestForTest(t *testing.T, DB *gorm.DB, admin *database.User, requestUUID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/admin/registration-requests/"+requestUUID+"/approve", nil)
	req.SetPathValue("request_uuid", requestUUID)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", admin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	ApproveRegistrationRequest(rr, req)
	return rr
}

// TestApproveRegistrationRequestNeverPersistsEmptyUsername is the ci#1158
// regression test for the admin sibling of the bot-create write path: approving
// two registration requests that carry no display name used to insert two
// `users` rows with `username = ”`, so the second approval tripped the unique
// index (`UNIQUE constraint failed: users.username` / 23505 on Postgres).
func TestApproveRegistrationRequestNeverPersistsEmptyUsername(t *testing.T) {
	DB := setupRegistrationUsernameTestDB(t)

	admin := database.User{Name: "admin", Username: "admin-ci1158", Email: "admin-ci1158@example.com", IsAdmin: true}
	if err := DB.Create(&admin).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	emails := []string{"first-ci1158@example.com", "second-ci1158@example.com"}
	for _, email := range emails {
		regRequest := database.RegistrationRequest{
			Name:         "", // no display name: the derivation must fall back
			Email:        email,
			PasswordHash: "x",
			Status:       database.RegistrationRequestStatusPending,
		}
		if err := DB.Create(&regRequest).Error; err != nil {
			t.Fatalf("failed to create registration request %q: %v", email, err)
		}

		rr := approveRegistrationRequestForTest(t, DB, &admin, regRequest.UUID)
		if rr.Code != 200 {
			t.Fatalf("ci#1158: approving %q must succeed, got %d: %s", email, rr.Code, rr.Body.String())
		}
		if body := strings.ToLower(rr.Body.String()); strings.Contains(body, "unique") || strings.Contains(body, "23505") {
			t.Fatalf("ci#1158: approval leaked a unique-constraint error: %s", rr.Body.String())
		}
	}

	var emptyUsernames int64
	if err := DB.Model(&database.User{}).Where("username = ?", "").Count(&emptyUsernames).Error; err != nil {
		t.Fatalf("failed to count empty usernames: %v", err)
	}
	if emptyUsernames != 0 {
		t.Fatalf("ci#1158: expected no user with an empty username, got %d", emptyUsernames)
	}

	var approved []database.User
	if err := DB.Where("email IN ?", emails).Find(&approved).Error; err != nil {
		t.Fatalf("failed to load approved users: %v", err)
	}
	if len(approved) != len(emails) {
		t.Fatalf("expected %d approved users, got %d", len(emails), len(approved))
	}
	seen := map[string]string{}
	for _, user := range approved {
		if strings.TrimSpace(user.Username) == "" {
			t.Fatalf("ci#1158: approved user %q persisted an empty username", user.Email)
		}
		if previous, exists := seen[user.Username]; exists {
			t.Fatalf("ci#1158: %q and %q share username %q", previous, user.Email, user.Username)
		}
		seen[user.Username] = user.Email
	}
}
