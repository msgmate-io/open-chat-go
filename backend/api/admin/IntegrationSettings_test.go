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
	"os"
	"path/filepath"
	"strings"
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

func TestListIntegrationSettingsIncludesCore(t *testing.T) {
	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings", nil, settingsAdminUser(t))
	rr := httptest.NewRecorder()

	ListIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Integrations []struct {
			Name       string `json:"name"`
			FieldCount int    `json:"field_count"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	found := false
	for _, integration := range payload.Integrations {
		if integration.Name == "core" {
			found = true
			if integration.FieldCount < 6 {
				t.Fatalf("expected core to expose bootstrap fields, got %d", integration.FieldCount)
			}
		}
	}
	if !found {
		t.Fatal("expected core in settings list")
	}
}

func TestGetIntegrationSettingsCoreExposesBootstrap(t *testing.T) {
	prevSource := runtimecfg.GetConfigSource()
	prevValues := runtimecfg.GetAll()
	t.Cleanup(func() {
		runtimecfg.SetConfigSource(prevSource)
		runtimecfg.SetAll(prevValues)
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "open-chat.json")
	if err := os.WriteFile(path, []byte(`{"env":{"PORT":"1984"},"bootstrap":{"git":{"owner":"admin"}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runtimecfg.SetConfigSource(path)
	runtimecfg.SetAll(map[string]runtimecfg.Value{"PORT": {Value: "1984"}})

	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings/core", nil, settingsAdminUser(t))
	req.SetPathValue("integration_name", "core")
	rr := httptest.NewRecorder()

	GetIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Integration struct {
			Fields []struct {
				Key          string `json:"key"`
				Group        string `json:"group"`
				Type         string `json:"type"`
				Value        string `json:"value"`
				ConfigTarget string `json:"config_target"`
			} `json:"fields"`
		} `json:"integration"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	byKey := map[string]struct {
		Group        string
		Type         string
		Value        string
		ConfigTarget string
	}{}
	for _, field := range payload.Integration.Fields {
		byKey[field.Key] = struct {
			Group        string
			Type         string
			Value        string
			ConfigTarget string
		}{field.Group, field.Type, field.Value, field.ConfigTarget}
	}
	git, ok := byKey["BOOTSTRAP_GIT"]
	if !ok {
		t.Fatal("expected BOOTSTRAP_GIT field")
	}
	if git.ConfigTarget != "bootstrap.git" || git.Group != "Bootstrap" {
		t.Fatalf("unexpected BOOTSTRAP_GIT descriptor: %#v", git)
	}
	if git.Value != `{"owner":"admin"}` {
		t.Fatalf("expected current bootstrap value, got %q", git.Value)
	}
	if _, ok := byKey["PORT"]; !ok {
		t.Fatal("expected PORT env field")
	}
}

func TestSaveIntegrationSettingsCoreRejectsInvalidBootstrap(t *testing.T) {
	req := settingsRequest(http.MethodPut, "/api/v1/admin/integration-settings/core",
		[]byte(`{"values":{"BOOTSTRAP_GIT":"not-json"}}`), settingsAdminUser(t))
	req.SetPathValue("integration_name", "core")
	rr := httptest.NewRecorder()

	SaveIntegrationSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid bootstrap JSON, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSaveIntegrationSettingsCorePersistsBootstrap(t *testing.T) {
	prevSource := runtimecfg.GetConfigSource()
	prevValues := runtimecfg.GetAll()
	t.Cleanup(func() {
		runtimecfg.SetConfigSource(prevSource)
		runtimecfg.SetAll(prevValues)
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "open-chat.json")
	if err := os.WriteFile(path, []byte(`{"env":{"PORT":"1984"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runtimecfg.SetConfigSource(path)
	runtimecfg.SetAll(map[string]runtimecfg.Value{"PORT": {Value: "1984"}})

	req := settingsRequest(http.MethodPut, "/api/v1/admin/integration-settings/core",
		[]byte(`{"values":{"BOOTSTRAP_GIT":"{\"owner\":\"admin\"}","PORT":"1985"}}`), settingsAdminUser(t))
	req.SetPathValue("integration_name", "core")
	rr := httptest.NewRecorder()

	SaveIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	bootstrap, _ := root["bootstrap"].(map[string]interface{})
	git, _ := bootstrap["git"].(map[string]interface{})
	if git["owner"] != "admin" {
		t.Fatalf("expected persisted bootstrap.git.owner admin, got %#v", root["bootstrap"])
	}
	env, _ := root["env"].(map[string]interface{})
	if env["PORT"] != "1985" {
		t.Fatalf("expected persisted env.PORT 1985, got %#v", root["env"])
	}
	if _, exists := env["BOOTSTRAP_GIT"]; exists {
		t.Fatal("did not expect BOOTSTRAP_GIT in env")
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

func rawSettingsState(t *testing.T, content string) (string, func()) {
	t.Helper()
	prevSource := runtimecfg.GetConfigSource()
	prevValues := runtimecfg.GetAll()
	dir := t.TempDir()
	path := filepath.Join(dir, "open-chat.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runtimecfg.SetConfigSource(path)
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"OCI_TEST_CORE_SETTINGS_HOST":  {Value: "orig-host"},
		"OCI_TEST_CORE_SETTINGS_TOKEN": {Value: "orig-secret", Sensitive: true},
	})
	return path, func() {
		runtimecfg.SetConfigSource(prevSource)
		runtimecfg.SetAll(prevValues)
	}
}

func TestGetRawIntegrationSettingsRequiresAdmin(t *testing.T) {
	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings/raw", nil, &database.User{IsAdmin: false})
	rr := httptest.NewRecorder()

	GetRawIntegrationSettings(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d", rr.Code)
	}
}

func TestGetRawIntegrationSettingsRedactsSecrets(t *testing.T) {
	ensureTestSettingsIntegration(t)
	path, cleanup := rawSettingsState(t, `{"env":{"OCI_TEST_CORE_SETTINGS_HOST":"orig-host","OCI_TEST_CORE_SETTINGS_TOKEN":"orig-secret"}}`)
	defer cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat config: %v", err)
	}

	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings/raw", nil, settingsAdminUser(t))
	rr := httptest.NewRecorder()
	GetRawIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload rawConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	body := payload.YAML
	if strings.Contains(body, "orig-secret") {
		t.Fatalf("expected secret redacted, got %s", body)
	}
	if !strings.Contains(body, "<redacted>") {
		t.Fatalf("expected redaction sentinel, got %s", body)
	}
	if !strings.Contains(body, "orig-host") {
		t.Fatalf("expected non-sensitive value visible, got %s", body)
	}
}

func TestGetRawIntegrationSettingsInMemoryFallback(t *testing.T) {
	ensureTestSettingsIntegration(t)
	prevSource := runtimecfg.GetConfigSource()
	prevValues := runtimecfg.GetAll()
	t.Cleanup(func() {
		runtimecfg.SetConfigSource(prevSource)
		runtimecfg.SetAll(prevValues)
	})
	runtimecfg.SetConfigSource("")
	runtimecfg.SetAll(map[string]runtimecfg.Value{"PORT": {Value: "1984"}})

	req := settingsRequest(http.MethodGet, "/api/v1/admin/integration-settings/raw", nil, settingsAdminUser(t))
	rr := httptest.NewRecorder()
	GetRawIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload rawConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.InMemory {
		t.Fatal("expected in_memory fallback when no config file is configured")
	}
	if !strings.Contains(payload.YAML, "PORT") {
		t.Fatalf("expected runtime values in fallback document, got %s", payload.YAML)
	}
}

func TestValidateRawIntegrationSettings(t *testing.T) {
	ensureTestSettingsIntegration(t)

	validate := func(t *testing.T, body string) rawConfigValidateResponse {
		t.Helper()
		req := settingsRequest(http.MethodPost, "/api/v1/admin/integration-settings/raw/validate",
			[]byte(body), settingsAdminUser(t))
		rr := httptest.NewRecorder()
		ValidateRawIntegrationSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var payload rawConfigValidateResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return payload
	}

	if got := validate(t, `{"yaml":"env:\n  OCI_TEST_CORE_SETTINGS_HOST: h\n"}`); !got.Valid {
		t.Fatalf("expected valid document, got errors %v", got.Errors)
	}
	if got := validate(t, `{"yaml":"foo: [1, 2"}`); got.Valid {
		t.Fatal("expected invalid YAML to be rejected")
	}
	if got := validate(t, `{"yaml":"integrations:\n  nope:\n    x: y\n"}`); got.Valid || !hasErrorContaining(got.Errors, "unknown integration") {
		t.Fatalf("expected unknown integration error, got %v", got.Errors)
	}
	if got := validate(t, `{"yaml":"integrations:\n  `+testSettingsIntegration+`:\n    NOPE: y\n"}`); got.Valid || !hasErrorContaining(got.Errors, "does not map to a declared runtime env var") {
		t.Fatalf("expected unknown key error, got %v", got.Errors)
	}
}

func hasErrorContaining(errs []string, needle string) bool {
	for _, err := range errs {
		if strings.Contains(err, needle) {
			return true
		}
	}
	return false
}

func TestSaveRawIntegrationSettingsPreservesRedacted(t *testing.T) {
	ensureTestSettingsIntegration(t)
	path, cleanup := rawSettingsState(t, `{"env":{"OCI_TEST_CORE_SETTINGS_HOST":"orig-host","OCI_TEST_CORE_SETTINGS_TOKEN":"orig-secret"}}`)
	defer cleanup()

	body := []byte(`{"yaml":"{\"env\":{\"OCI_TEST_CORE_SETTINGS_HOST\":\"new-host\",\"OCI_TEST_CORE_SETTINGS_TOKEN\":\"<redacted>\"}}"}`)
	req := settingsRequest(http.MethodPut, "/api/v1/admin/integration-settings/raw", body, settingsAdminUser(t))
	rr := httptest.NewRecorder()
	SaveRawIntegrationSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload rawConfigSaveResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Persisted {
		t.Fatalf("expected persisted, got persist_error %q", payload.PersistError)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	env := root["env"].(map[string]interface{})
	if env["OCI_TEST_CORE_SETTINGS_HOST"] != "new-host" {
		t.Fatalf("expected updated host, got %#v", env)
	}
	if env["OCI_TEST_CORE_SETTINGS_TOKEN"] != "orig-secret" {
		t.Fatalf("expected redacted secret restored from disk, got %#v", env)
	}
}

func TestSaveRawIntegrationSettingsRejectsInvalid(t *testing.T) {
	ensureTestSettingsIntegration(t)
	req := settingsRequest(http.MethodPut, "/api/v1/admin/integration-settings/raw",
		[]byte(`{"yaml":"integrations:\n  nope:\n    x: y\n"}`), settingsAdminUser(t))
	rr := httptest.NewRecorder()
	SaveRawIntegrationSettings(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid document, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDownloadRawIntegrationSettings(t *testing.T) {
	path, cleanup := rawSettingsState(t, `{"env":{"OCI_TEST_CORE_SETTINGS_HOST":"orig-host"}}`)
	defer cleanup()
	user := settingsAdminUser(t)

	wrong := settingsRequest(http.MethodPost, "/api/v1/admin/integration-settings/raw/download",
		[]byte(`{"password":"wrong"}`), user)
	wrongRR := httptest.NewRecorder()
	DownloadRawIntegrationSettings(wrongRR, wrong)
	if wrongRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", wrongRR.Code)
	}

	right := settingsRequest(http.MethodPost, "/api/v1/admin/integration-settings/raw/download",
		[]byte(`{"password":"secret-password"}`), user)
	rightRR := httptest.NewRecorder()
	DownloadRawIntegrationSettings(rightRR, right)
	if rightRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct password, got %d: %s", rightRR.Code, rightRR.Body.String())
	}
	if disposition := rightRR.Header().Get("Content-Disposition"); !strings.Contains(disposition, filepath.Base(path)) {
		t.Fatalf("expected attachment filename in Content-Disposition, got %q", disposition)
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Equal(rightRR.Body.Bytes(), expected) {
		t.Fatalf("expected downloaded bytes to match config file")
	}
}

func TestDownloadRawIntegrationSettingsNoFile(t *testing.T) {
	prevSource := runtimecfg.GetConfigSource()
	t.Cleanup(func() { runtimecfg.SetConfigSource(prevSource) })
	runtimecfg.SetConfigSource("")

	req := settingsRequest(http.MethodPost, "/api/v1/admin/integration-settings/raw/download",
		[]byte(`{"password":"secret-password"}`), settingsAdminUser(t))
	rr := httptest.NewRecorder()
	DownloadRawIntegrationSettings(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 when no config file, got %d: %s", rr.Code, rr.Body.String())
	}
}
