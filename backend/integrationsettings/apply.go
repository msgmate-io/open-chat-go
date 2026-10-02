package integrationsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// ConfigDefinitions returns the registered integration definitions together
// with the synthetic core definition. It mirrors what the admin settings API
// exposes for editing so raw config validation/redaction is consistent between
// the HTTP layer and integration tools.
func ConfigDefinitions() []integrationinterface.Definition {
	defs := integrationinterface.List()
	defs = append(defs, CoreDefinition())
	return defs
}

// ApplyValidationError carries the per-field validation problems of a rejected
// raw config document.
type ApplyValidationError struct {
	Errors []string
}

func (e *ApplyValidationError) Error() string {
	return strings.Join(e.Errors, "; ")
}

// ApplyResult reports how a raw config document was persisted.
type ApplyResult struct {
	Deployment      DeploymentInfo
	Persisted       bool
	PersistError    string
	RestartRequired bool
	RemotePersisted bool
	RemoteTarget    string
	RemoteError     string
}

// ApplyRawConfigDocument validates a raw config document and persists it to the
// local config source, restoring values that were sent back using the redaction
// sentinel. When a deployment-host kubernetes Secret is configured the same
// document is mirrored there. A read-only local mount is tolerated as long as
// the remote mirror succeeds.
//
// Validation failures are returned as *ApplyValidationError. Parse and other
// hard failures are returned as plain errors.
func ApplyRawConfigDocument(source string, yaml string) (ApplyResult, error) {
	defs := ConfigDefinitions()

	root, err := ParseConfigDocument(yaml)
	if err != nil {
		return ApplyResult{}, err
	}
	if errs := ValidateConfigDocument(root, defs); len(errs) > 0 {
		return ApplyResult{}, &ApplyValidationError{Errors: errs}
	}

	result := ApplyResult{
		Deployment:      BuildDeploymentInfo(),
		RestartRequired: true,
	}

	// Load the current document so values sent back as the redaction sentinel
	// can be restored. When the local source is not persistable (inline config,
	// read-only mount) we fall back to the effective runtime values so redacted
	// secrets are still restored and the document can be mirrored remotely.
	var original map[string]interface{}
	format := normalizedConfigFormat(source)
	if loaded, sourceFormat, loadErr := LoadConfigDocument(source); loadErr == nil {
		original = loaded
		format = sourceFormat
	} else if !errors.Is(loadErr, ErrNotPersistable) {
		return ApplyResult{}, loadErr
	} else {
		original = BuildRuntimeConfigDocument(defs, runtimecfg.GetAll())
	}

	restored := RestoreRedactedConfig(original, root, runtimecfg.GetAll(), defs)

	if _, err := SaveConfigDocument(source, restored); err != nil {
		result.PersistError = err.Error()
	} else {
		result.Persisted = true
	}

	if encoded, encErr := EncodeConfigDocument(restored, format); encErr != nil {
		result.RemoteError = encErr.Error()
	} else if target, configured, remoteErr := PersistRemoteConfig(encoded); remoteErr != nil {
		if !errors.Is(remoteErr, ErrNoRemoteConfigTarget) {
			result.RemoteError = remoteErr.Error()
		}
	} else if configured {
		result.RemotePersisted = true
		result.RemoteTarget = target
		result.Persisted = true
	}

	// Best-effort live re-apply so most settings take effect without a restart.
	// Startup-only keys (bootstrap) are still handled by the rollout restart.
	ApplyDocumentValues(restored, defs)

	if result.Persisted {
		// A local write failure is only a problem when the remote mirror also
		// failed.
		result.PersistError = ""
	} else if result.PersistError == "" {
		if result.RemoteError != "" {
			result.PersistError = result.RemoteError
		} else {
			result.PersistError = ErrNotPersistable.Error()
		}
	}

	return result, nil
}

// ApplyDocumentValues best-effort applies a config document's env and
// per-integration values to the live runtime config and process environment so
// most settings take effect without a restart. It mirrors flattenConfigEnv's
// key resolution. Bootstrap/startup-only keys cannot be applied here.
func ApplyDocumentValues(root map[string]interface{}, defs []integrationinterface.Definition) {
	if root == nil {
		return
	}

	if env, ok := root["env"].(map[string]interface{}); ok {
		for key, value := range env {
			normalized := normalizeKey(key)
			if normalized == "" || value == nil {
				continue
			}
			rendered := stringifyDocumentValue(value)
			runtimecfg.SetValue(normalized, runtimecfg.Value{Value: rendered})
			_ = os.Setenv(normalized, rendered)
		}
	}

	integrationsRaw, ok := root["integrations"].(map[string]interface{})
	if !ok {
		return
	}
	byName := map[string]integrationinterface.Definition{}
	for _, def := range defs {
		byName[strings.ToLower(strings.TrimSpace(def.Name))] = def
	}
	for name, sectionRaw := range integrationsRaw {
		def, known := byName[strings.ToLower(strings.TrimSpace(name))]
		if !known {
			continue
		}
		section, ok := sectionRaw.(map[string]interface{})
		if !ok {
			continue
		}
		for key, value := range section {
			envKey := resolveEnvKey(def, key)
			if envKey == "" || value == nil {
				continue
			}
			rendered := stringifyDocumentValue(value)
			runtimecfg.SetValue(envKey, runtimecfg.Value{
				Value:     rendered,
				Sensitive: isSensitiveKey(def, envKey),
			})
			_ = os.Setenv(envKey, rendered)
		}
	}
}

// stringifyDocumentValue renders a decoded config value for an environment
// variable. Structured values are JSON-encoded.
func stringifyDocumentValue(value interface{}) string {
	switch value.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, nil:
		return fmt.Sprintf("%v", value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}
		return string(encoded)
	}
}
