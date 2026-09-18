package integrationsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// ErrNotPersistable indicates that the active config source cannot be written
// back to disk (inline config, YAML, missing file, or read-only path).
var ErrNotPersistable = errors.New("configuration is not persistable")

var configFileMu sync.Mutex

func isSensitiveKey(def integrationinterface.Definition, key string) bool {
	normalized := normalizeKey(key)
	for _, decl := range def.RuntimeEnvVars {
		if normalizeKey(decl.Key) == normalized {
			return decl.Sensitive
		}
	}
	return false
}

// MergeValues writes the given values into the JSON config document at path.
// A nil value removes the key. Unknown top-level keys are preserved. The write
// is atomic (temp file + rename) and keeps the original file permissions.
func MergeValues(path string, def integrationinterface.Definition, values map[string]*string) error {
	configFileMu.Lock()
	defer configFileMu.Unlock()

	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" || strings.HasPrefix(trimmedPath, "inline") {
		return fmt.Errorf("%w: no writable config path", ErrNotPersistable)
	}

	original, err := os.ReadFile(trimmedPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotPersistable, err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(original, &root); err != nil {
		return fmt.Errorf("%w: config is not a JSON object", ErrNotPersistable)
	}
	if root == nil {
		root = map[string]interface{}{}
	}

	envSection, _ := root["env"].(map[string]interface{})
	if envSection == nil {
		envSection = map[string]interface{}{}
	}
	integrationsSection, _ := root["integrations"].(map[string]interface{})
	if integrationsSection == nil {
		integrationsSection = map[string]interface{}{}
	}
	integrationSection, _ := integrationsSection[def.Name].(map[string]interface{})
	if integrationSection == nil {
		integrationSection = map[string]interface{}{}
	}

	aliasByEnvKey := map[string]string{}
	for _, alias := range def.RuntimeConfigAliases {
		envKey := normalizeKey(alias.EnvKey)
		jsonKey := strings.TrimSpace(alias.JSONKey)
		if envKey == "" || jsonKey == "" {
			continue
		}
		aliasByEnvKey[envKey] = jsonKey
	}

	for rawKey, rawValue := range values {
		key := normalizeKey(rawKey)
		if key == "" {
			continue
		}
		fieldType := inferredTypeForKey(def, key)
		jsonKey, hasAlias := aliasByEnvKey[key]

		if rawValue == nil {
			delete(envSection, key)
			if hasAlias {
				delete(integrationSection, jsonKey)
			}
			continue
		}

		if hasAlias {
			integrationSection[jsonKey] = coerceConfigValue(fieldType, *rawValue)
			// Avoid a stale env entry overriding nothing but adding ambiguity.
			delete(envSection, key)
		} else {
			envSection[key] = *rawValue
		}
	}

	if len(envSection) > 0 {
		root["env"] = envSection
	} else {
		delete(root, "env")
	}
	if len(integrationSection) > 0 {
		integrationsSection[def.Name] = integrationSection
	} else {
		delete(integrationsSection, def.Name)
	}
	if len(integrationsSection) > 0 {
		root["integrations"] = integrationsSection
	} else {
		delete(root, "integrations")
	}

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	encoded = append(encoded, '\n')

	info, err := os.Stat(trimmedPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotPersistable, err)
	}
	perm := info.Mode().Perm()

	dir := filepath.Dir(trimmedPath)
	tmp, err := os.CreateTemp(dir, ".open-chat-config-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp config: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set temp config permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp config: %w", err)
	}
	if err := os.Rename(tmpName, trimmedPath); err != nil {
		return fmt.Errorf("failed to replace config: %w", err)
	}
	return nil
}

func inferredTypeForKey(def integrationinterface.Definition, key string) string {
	normalized := normalizeKey(key)
	for _, decl := range def.RuntimeEnvVars {
		if normalizeKey(decl.Key) == normalized {
			return InferFieldType(decl, "")
		}
	}
	return FieldTypeString
}

func coerceConfigValue(fieldType string, raw string) interface{} {
	switch fieldType {
	case FieldTypeBool:
		if parsed, err := parseBool(raw); err == nil {
			return parsed
		}
	case FieldTypeNumber:
		var num json.Number
		if err := json.Unmarshal([]byte(raw), &num); err == nil {
			return num
		}
	case FieldTypeJSON:
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" && json.Valid([]byte(trimmed)) {
			var parsed interface{}
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				return parsed
			}
		}
	}
	return raw
}
