package integrations

import (
	"backend/database"
	"backend/server/util"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupIntegrationsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "integrations_access_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createUserForIntegrationsTest(t *testing.T, db *gorm.DB, email string, isAdmin bool) *database.User {
	t.Helper()
	err, user := util.CreateUser(db, email, "Passw0rd!", isAdmin)
	if err != nil {
		t.Fatalf("failed to create user %q: %v", email, err)
	}
	return user
}

func requestWithDBUser(path string, db *gorm.DB, user *database.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	ctx := context.WithValue(req.Context(), "db", db)
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	h := &IntegrationsHandler{}
	h.List(rr, req)
	return rr
}

func TestListShowsOnlyUserVisibleIntegrationsForRegularUsers(t *testing.T) {
	db := setupIntegrationsTestDB(t)
	user := createUserForIntegrationsTest(t, db, "regular@example.com", false)

	rr := requestWithDBUser("/api/v1/integrations/list", db, user)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var payload IntegrationsListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}

	names := map[string]IntegrationListRow{}
	for _, row := range payload.Rows {
		names[row.Name] = row
	}
	if _, exists := names["ssh"]; !exists {
		t.Fatalf("expected ssh to be visible for regular users")
	}
	if _, exists := names["account_management"]; exists {
		t.Fatalf("expected account_management to be hidden for regular users")
	}
	if _, exists := names["admin"]; exists {
		t.Fatalf("expected admin integration to be hidden for regular users")
	}
	if _, exists := names["rest_api_tool"]; !exists {
		t.Fatalf("expected rest_api_tool to be visible for regular users")
	}
	if _, exists := names["mcp"]; !exists {
		t.Fatalf("expected mcp to be visible for regular users")
	}
}

func TestGrantAccessRejectsAdminOnlyForRegularUser(t *testing.T) {
	db := setupIntegrationsTestDB(t)
	admin := createUserForIntegrationsTest(t, db, "admin@example.com", true)
	target := createUserForIntegrationsTest(t, db, "regular@example.com", false)

	req := httptest.NewRequest("POST", "/api/v1/admin/integrations/access/target/admin", nil)
	req.SetPathValue("user_uuid", target.UUID)
	req.SetPathValue("integration_name", "admin")
	ctx := context.WithValue(req.Context(), "db", db)
	ctx = context.WithValue(ctx, "user", admin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &IntegrationsHandler{}
	h.GrantAccess(rr, req)

	if rr.Code != 400 {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminOnlyIntegrationStaysHiddenForRegularUserEvenIfAssigned(t *testing.T) {
	db := setupIntegrationsTestDB(t)
	user := createUserForIntegrationsTest(t, db, "regular@example.com", false)
	if err := database.EnsureIntegrationAccess(db, user.ID, "admin"); err != nil {
		t.Fatalf("failed to grant admin integration access: %v", err)
	}

	rr := requestWithDBUser("/api/v1/integrations/list", db, user)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var payload IntegrationsListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}

	foundAdmin := false
	for _, row := range payload.Rows {
		if row.Name == "admin" {
			foundAdmin = true
			break
		}
	}
	if foundAdmin {
		t.Fatalf("expected admin integration to remain hidden for regular users")
	}
}
