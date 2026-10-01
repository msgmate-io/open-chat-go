package integrationsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// ValidationError describes a rejected settings value.
type ValidationError struct {
	Key     string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Key == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Key, e.Message)
}

func parseBool(raw string) (bool, error) {
	return strconv.ParseBool(strings.TrimSpace(raw))
}

func parseNumber(raw string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(raw), 64)
}

func formatBound(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func containsString(options []string, value string) bool {
	for _, option := range options {
		if strings.TrimSpace(option) == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}

// ValidateValues checks that every key is declared by the integration and that
// values conform to the declared/inferred field type. It returns a normalized
// copy keyed by uppercase env key. A nil value means "unset".
func ValidateValues(def integrationinterface.Definition, input map[string]*string) (map[string]*string, error) {
	declared := map[string]integrationinterface.RuntimeEnvVar{}
	for _, decl := range def.RuntimeEnvVars {
		key := normalizeKey(decl.Key)
		if key != "" {
			declared[key] = decl
		}
	}

	out := make(map[string]*string, len(input))
	for rawKey, rawValue := range input {
		key := normalizeKey(rawKey)
		if key == "" {
			return nil, &ValidationError{Message: "empty field key"}
		}
		decl, ok := declared[key]
		if !ok {
			return nil, &ValidationError{Key: key, Message: "unknown field for integration"}
		}
		if rawValue == nil {
			if decl.Required {
				return nil, &ValidationError{Key: key, Message: "is required"}
			}
			out[key] = nil
			continue
		}

		value := *rawValue
		if decl.Required && strings.TrimSpace(value) == "" {
			return nil, &ValidationError{Key: key, Message: "is required"}
		}
		if def.Name == CoreSettingsName && isCoreBootstrapKey(key) {
			if !json.Valid([]byte(strings.TrimSpace(value))) {
				return nil, &ValidationError{Key: key, Message: "expected valid JSON"}
			}
			out[key] = &value
			continue
		}
		fieldType := InferFieldType(decl, "")
		switch fieldType {
		case FieldTypeBool:
			parsed, err := parseBool(value)
			if err != nil {
				return nil, &ValidationError{Key: key, Message: "expected a boolean value"}
			}
			normalized := strconv.FormatBool(parsed)
			out[key] = &normalized
		case FieldTypeNumber:
			parsed, err := parseNumber(value)
			if err != nil {
				return nil, &ValidationError{Key: key, Message: "expected a numeric value"}
			}
			if decl.Min != nil && parsed < *decl.Min {
				return nil, &ValidationError{Key: key, Message: fmt.Sprintf("must be at least %s", formatBound(*decl.Min))}
			}
			if decl.Max != nil && parsed > *decl.Max {
				return nil, &ValidationError{Key: key, Message: fmt.Sprintf("must be at most %s", formatBound(*decl.Max))}
			}
			out[key] = &value
		case FieldTypeSelect:
			if len(decl.Options) > 0 && !containsString(decl.Options, value) {
				return nil, &ValidationError{Key: key, Message: fmt.Sprintf("must be one of: %s", strings.Join(decl.Options, ", "))}
			}
			out[key] = &value
		default:
			out[key] = &value
		}
	}
	return out, nil
}

// ApplyValues updates the in-memory runtime config and process environment for
// the given values. A nil value unsets the key.
func ApplyValues(def integrationinterface.Definition, values map[string]*string) {
	for rawKey, rawValue := range values {
		key := normalizeKey(rawKey)
		if key == "" {
			continue
		}
		// Bootstrap changes are restart-only: they are persisted to the config
		// file but cannot be applied to the running process.
		if def.Name == CoreSettingsName && isCoreBootstrapKey(key) {
			continue
		}
		if rawValue == nil {
			runtimecfg.DeleteValue(key)
			_ = os.Unsetenv(key)
			continue
		}
		runtimecfg.SetValue(key, runtimecfg.Value{
			Value:     *rawValue,
			Sensitive: isSensitiveKey(def, key),
		})
		_ = os.Setenv(key, *rawValue)
	}
}

// PersistValues writes the given values back to the active config file (JSON
// or YAML).
func PersistValues(def integrationinterface.Definition, values map[string]*string) error {
	return MergeValues(runtimecfg.GetConfigSource(), def, values)
}

// PersistOutcome reports how per-integration settings values were persisted
// locally and, when configured, to the remote deployment-host backing store.
type PersistOutcome struct {
	Deployment      DeploymentInfo
	Persisted       bool
	PersistError    string
	RemotePersisted bool
	RemoteTarget    string
	RemoteError     string
}

// normalizedConfigFormat maps a config source to a concrete encoding ("json" or
// "yaml") for the remote mirror, defaulting to YAML for inline/unknown sources.
func normalizedConfigFormat(source string) string {
	if DetectConfigFormat(source) == "json" {
		return "json"
	}
	return "yaml"
}

// PersistValuesWithRemote writes the given values back to the active config
// file when it is writable *and* mirrors the resulting document to the
// registered remote backing store (the deployment-host kubernetes Secret). When
// the local source is not persistable (inline config, read-only mount) the
// document is synthesized from the effective runtime values so the remote
// mirror still works. A successful local *or* remote write marks the outcome
// persisted.
func PersistValuesWithRemote(def integrationinterface.Definition, values map[string]*string) PersistOutcome {
	source := runtimecfg.GetConfigSource()
	outcome := PersistOutcome{Deployment: BuildDeploymentInfo()}

	root, format, err := LoadConfigDocument(source)
	if err != nil {
		if !errors.Is(err, ErrNotPersistable) {
			outcome.PersistError = err.Error()
			return outcome
		}
		root = BuildRuntimeConfigDocument(ConfigDefinitions(), runtimecfg.GetAll())
		format = normalizedConfigFormat(source)
	}

	merged := mergeValuesIntoDocument(root, def, values)

	if _, err := SaveConfigDocument(source, merged); err != nil {
		outcome.PersistError = err.Error()
	} else {
		outcome.Persisted = true
	}

	if encoded, encErr := EncodeConfigDocument(merged, format); encErr != nil {
		outcome.RemoteError = encErr.Error()
	} else if target, configured, remoteErr := PersistRemoteConfig(encoded); remoteErr != nil {
		if !errors.Is(remoteErr, ErrNoRemoteConfigTarget) {
			outcome.RemoteError = remoteErr.Error()
		}
	} else if configured {
		outcome.RemotePersisted = true
		outcome.RemoteTarget = target
		outcome.Persisted = true
	}

	if outcome.Persisted {
		outcome.PersistError = ""
	} else if outcome.PersistError == "" {
		if outcome.RemoteError != "" {
			outcome.PersistError = outcome.RemoteError
		} else {
			outcome.PersistError = ErrNotPersistable.Error()
		}
	}

	return outcome
}
