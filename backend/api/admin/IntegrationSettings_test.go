package admin

import (
	"backend/database"
	"backend/runtimecfg"
	"backend/servicecontrol"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const testSettingsIntegration = "test_core_settings_integration"

func ensureTestSettingsIntegration(t *testing.T) {
	t.Helper()
	if _, ok := integrationinterface.Get(testSettingsIntegration); ok {
		return
	}
	err := integrationinterface.Register(integrationinterface.Definition{
		Name: testSettingsIntegration,
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_TEST_CORE_SETTINGS_HOST"},
			{Key: "OCI_TEST_CORE_SETTINGS_TOKEN", Sensitive: true},
		},
	})
	if err != nil {
		t.Fatalf("register test integration: %v", err)
	}
}

func settingsRequest(method, target string, body []byte, user *database.User) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), "db", (*gorm.DB)(nil))
	ctx = context.WithValue(ctx, "user", user)
	return req.WithContext(ctx)
}

func settingsAdminUser(t *testing.T) *database.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return &database.User{IsAdmin: true, PasswordHash: string(hash)}
}

func TestListIntegrationSettingsRequiresAdmin(t *testing.T) {
	ensureTestSettingsIntegration(t)
	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings", nil, &database.User{IsAdmin: false})
	rr := httptest.NewRecorder()

	ListIntegrationSettings(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d", rr.Code)
	}
}

func TestListIntegrationSettingsIncludesDeclaredIntegration(t *testing.T) {
	ensureTestSettingsIntegration(t)
	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings", nil, settingsAdminUser(t))
	rr := httptest.NewRecorder()

	ListIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Integrations []struct {
			Name string `json:"name"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	found := false
	for _, integration := range payload.Integrations {
		if integration.Name == testSettingsIntegration {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %s in settings list", testSettingsIntegration)
	}
}

func TestGetIntegrationSettingsUnknownIntegration(t *testing.T) {
	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings/nope", nil, settingsAdminUser(t))
	req.SetPathValue("integration_name", "nope")
	rr := httptest.NewRecorder()

	GetIntegrationSettings(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestSaveIntegrationSettingsRejectsUnknownKey(t *testing.T) {
	ensureTestSettingsIntegration(t)
	req := settingsRequest(http.MethodPut, "/api/v1/admin/integration-settings/"+testSettingsIntegration,
		[]byte(`{"values":{"OCI_UNKNOWN_KEY":"x"}}`), settingsAdminUser(t))
	req.SetPathValue("integration_name", testSettingsIntegration)
	rr := httptest.NewRecorder()

	SaveIntegrationSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown key, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSaveIntegrationSettingsAppliesValue(t *testing.T) {
	ensureTestSettingsIntegration(t)
	runtimecfg.SetConfigSource("")
	t.Cleanup(func() { runtimecfg.DeleteValue("OCI_TEST_CORE_SETTINGS_HOST") })

	req := settingsRequest(http.MethodPut, "/api/v1/admin/integration-settings/"+testSettingsIntegration,
		[]byte(`{"values":{"OCI_TEST_CORE_SETTINGS_HOST":"example.test"}}`), settingsAdminUser(t))
	req.SetPathValue("integration_name", testSettingsIntegration)
	rr := httptest.NewRecorder()

	SaveIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := runtimecfg.GetAll()["OCI_TEST_CORE_SETTINGS_HOST"].Value; got != "example.test" {
		t.Fatalf("expected applied value, got %q", got)
	}
}

func TestRevealIntegrationSettingsPassword(t *testing.T) {
	ensureTestSettingsIntegration(t)
	runtimecfg.SetValue("OCI_TEST_CORE_SETTINGS_TOKEN", runtimecfg.Value{Value: "top-secret", Sensitive: true})
	t.Cleanup(func() { runtimecfg.DeleteValue("OCI_TEST_CORE_SETTINGS_TOKEN") })
	user := settingsAdminUser(t)

	wrong := settingsRequest(http.MethodPost, "/api/v1/admin/integration-settings/"+testSettingsIntegration+"/reveal",
		[]byte(`{"password":"wrong"}`), user)
	wrong.SetPathValue("integration_name", testSettingsIntegration)
	wrongRR := httptest.NewRecorder()
	RevealIntegrationSettings(wrongRR, wrong)
	if wrongRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", wrongRR.Code)
	}

	right := settingsRequest(http.MethodPost, "/api/v1/admin/integration-settings/"+testSettingsIntegration+"/reveal",
		[]byte(`{"password":"secret-password"}`), user)
	right.SetPathValue("integration_name", testSettingsIntegration)
	rightRR := httptest.NewRecorder()
	RevealIntegrationSettings(rightRR, right)
	if rightRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct password, got %d: %s", rightRR.Code, rightRR.Body.String())
	}
	if !bytes.Contains(rightRR.Body.Bytes(), []byte("top-secret")) {
		t.Fatalf("expected revealed secret in response, got %s", rightRR.Body.String())
	}
}

func TestRestartServerUnsupported(t *testing.T) {
	if servicecontrol.RestartSupported() {
		t.Skip("service manager available; unsupported path cannot be exercised")
	}
	req := settingsRequest(http.MethodPost, "/api/v1/admin/integration-settings/restart", nil, settingsAdminUser(t))
	rr := httptest.NewRecorder()

	RestartServer(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 when restart unsupported, got %d: %s", rr.Code, rr.Body.String())
	}
}
