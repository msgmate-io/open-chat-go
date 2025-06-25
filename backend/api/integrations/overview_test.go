package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOverviewIncludesIntegrationDefaultBots(t *testing.T) {
	db := setupIntegrationsTestDB(t)
	admin := createUserForIntegrationsTest(t, db, "admin.overview@example.com", true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/ssh/overview", nil)
	req.SetPathValue("integration_name", "ssh")
	ctx := context.WithValue(req.Context(), "db", db)
	ctx = context.WithValue(ctx, "user", admin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &IntegrationsHandler{}
	h.Overview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var payload IntegrationOverviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode overview response: %v", err)
	}
	if payload.Name != "ssh" {
		t.Fatalf("expected integration name ssh, got %q", payload.Name)
	}
	if len(payload.DefaultBots) == 0 {
		t.Fatalf("expected ssh integration overview to include default_bots")
	}
	first := payload.DefaultBots[0]
	if first.Username != "ssh-bot" {
		t.Fatalf("expected default bot username ssh-bot, got %q", first.Username)
	}
	if !first.UsesRandomPassword {
		t.Fatalf("expected default bot to indicate uses_random_password=true")
	}
}

func TestListIncludesDefaultBotCount(t *testing.T) {
	db := setupIntegrationsTestDB(t)
	admin := createUserForIntegrationsTest(t, db, "admin.list@example.com", true)

	rr := requestWithDBUser("/api/v1/integrations/list", db, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var payload IntegrationsListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}

	foundSSH := false
	for _, row := range payload.Rows {
		if row.Name != "ssh" {
			continue
		}
		foundSSH = true
		if row.DefaultBotCount < 1 {
			t.Fatalf("expected ssh default_bot_count >= 1, got %d", row.DefaultBotCount)
		}
	}
	if !foundSSH {
		t.Fatalf("expected ssh row in integration list for admin")
	}
}
