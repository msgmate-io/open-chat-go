package integrationsettings

import (
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
			out[key] = nil
			continue
		}

		value := *rawValue
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
			if _, err := parseNumber(value); err != nil {
				return nil, &ValidationError{Key: key, Message: "expected a numeric value"}
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
