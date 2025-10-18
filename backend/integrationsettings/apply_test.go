package integrationsettings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

const applyTestIntegration = "test_apply_settings_integration"

func ensureApplyTestIntegration(t *testing.T) {
	t.Helper()
	if _, ok := integrationinterface.Get(applyTestIntegration); ok {
		return
	}
	err := integrationinterface.Register(integrationinterface.Definition{
		Name: applyTestIntegration,
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_TEST_APPLY_HOST"},
			{Key: "OCI_TEST_APPLY_TOKEN", Sensitive: true},
		},
	})
	if err != nil {
		t.Fatalf("register test integration: %v", err)
	}
}

func applyTestState(t *testing.T, content string) string {
	t.Helper()
	prevSource := runtimecfg.GetConfigSource()
	prevValues := runtimecfg.GetAll()
	t.Cleanup(func() {
		runtimecfg.SetConfigSource(prevSource)
		runtimecfg.SetAll(prevValues)
		RegisterRemoteConfigPersister(nil)
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "open-chat.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runtimecfg.SetConfigSource(path)
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"OCI_TEST_APPLY_HOST":  {Value: "orig-host"},
		"OCI_TEST_APPLY_TOKEN": {Value: "orig-secret", Sensitive: true},
	})
	return path
}

func TestApplyRawConfigDocumentRestoresRedactedValues(t *testing.T) {
	ensureApplyTestIntegration(t)
	path := applyTestState(t, `{"env":{"OCI_TEST_APPLY_HOST":"orig-host","OCI_TEST_APPLY_TOKEN":"orig-secret"}}`)

	result, err := ApplyRawConfigDocument(path,
		`{"env":{"OCI_TEST_APPLY_HOST":"new-host","OCI_TEST_APPLY_TOKEN":"<redacted>"}}`)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !result.Persisted {
		t.Fatalf("expected persisted, got persist_error %q", result.PersistError)
	}
	if !result.RestartRequired {
		t.Fatal("expected restart_required")
	}
	if result.RemotePersisted {
		t.Fatal("did not expect remote persist without a persister")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), "orig-secret") {
		t.Fatalf("expected redacted secret restored from disk, got %s", raw)
	}
	if !strings.Contains(string(raw), "new-host") {
		t.Fatalf("expected updated host, got %s", raw)
	}
}

func TestApplyRawConfigDocumentMirrorsRemoteConfig(t *testing.T) {
	ensureApplyTestIntegration(t)
	path := applyTestState(t, `{"env":{"OCI_TEST_APPLY_HOST":"orig-host"}}`)

	var mirrored []byte
	RegisterRemoteConfigPersister(func(data []byte) (string, error) {
		mirrored = append([]byte(nil), data...)
		return "k8s-secret/open-chat-config", nil
	})

	result, err := ApplyRawConfigDocument(path, `{"env":{"OCI_TEST_APPLY_HOST":"new-host"}}`)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !result.RemotePersisted || result.RemoteTarget != "k8s-secret/open-chat-config" {
		t.Fatalf("expected remote persist, got %#v", result)
	}
	if len(mirrored) == 0 {
		t.Fatal("expected remote persister to receive the encoded document")
	}
}

func TestApplyRawConfigDocumentRejectsInvalidDocument(t *testing.T) {
	ensureApplyTestIntegration(t)
	path := applyTestState(t, `{"env":{"OCI_TEST_APPLY_HOST":"orig-host"}}`)

	_, err := ApplyRawConfigDocument(path, `integrations:\n  nope:\n    x: y\n`)
	if err == nil {
		t.Fatal("expected validation error")
	}
	var validationErr *ApplyValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected *ApplyValidationError, got %T: %v", err, err)
	}
	if len(validationErr.Errors) == 0 {
		t.Fatal("expected validation error details")
	}
}
