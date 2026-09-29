package integrationsettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	goyaml "go.yaml.in/yaml/v3"
)

// ErrNotPersistable indicates that the active config source cannot be written
// back to disk (inline config, missing file, unsupported format, or read-only
// path).
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

// MergeValues writes the given values into the config document at path. A nil
// value removes the key. Unknown top-level keys are preserved. JSON and YAML
// documents are both supported; the document is written back in its original
// format. The write is atomic (temp file + rename) and keeps the original file
// permissions.
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

	root, format, err := decodeConfigDocument(original)
	if err != nil {
		return err
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

		if def.Name == CoreSettingsName {
			if section, ok := coreBootstrapSectionForKey(key); ok {
				bootstrapSection, _ := root["bootstrap"].(map[string]interface{})
				if bootstrapSection == nil {
					bootstrapSection = map[string]interface{}{}
				}
				if rawValue == nil {
					delete(bootstrapSection, section)
				} else {
					bootstrapSection[section] = coerceConfigValue(FieldTypeJSON, *rawValue)
				}
				if len(bootstrapSection) > 0 {
					root["bootstrap"] = bootstrapSection
				} else {
					delete(root, "bootstrap")
				}
				continue
			}
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

	encoded, err := encodeConfigDocument(root, format)
	if err != nil {
		return err
	}

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
		trimmed := strings.TrimSpace(raw)
		if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return f
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

// decodeConfigDocument parses a config document as JSON when possible and falls
// back to YAML otherwise. It returns the decoded root mapping and the detected
// format ("json" or "yaml") so the document can be written back unchanged.
func decodeConfigDocument(raw []byte) (map[string]interface{}, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]interface{}{}, "json", nil
	}

	var jsonRoot map[string]interface{}
	if err := json.Unmarshal(trimmed, &jsonRoot); err == nil {
		if jsonRoot == nil {
			jsonRoot = map[string]interface{}{}
		}
		return jsonRoot, "json", nil
	}

	var yamlRoot map[string]interface{}
	if err := goyaml.Unmarshal(trimmed, &yamlRoot); err != nil {
		return nil, "", fmt.Errorf("%w: config is neither a JSON nor a YAML object", ErrNotPersistable)
	}
	if yamlRoot == nil {
		yamlRoot = map[string]interface{}{}
	}
	return yamlRoot, "yaml", nil
}

// encodeConfigDocument serializes a config document in the given format.
func encodeConfigDocument(root map[string]interface{}, format string) ([]byte, error) {
	if format == "yaml" {
		encoded, err := goyaml.Marshal(root)
		if err != nil {
			return nil, fmt.Errorf("failed to encode YAML config: %w", err)
		}
		return encoded, nil
	}
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode config: %w", err)
	}
	encoded = append(encoded, '\n')
	return encoded, nil
}
