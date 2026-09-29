package integrationsettings

import (
	"fmt"
	"os"
	"strings"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// RedactedValue is the sentinel written in place of sensitive values in the raw
// config view. When a document containing the sentinel is saved, the original
// value is restored from disk instead of persisting the sentinel itself.
const RedactedValue = "<redacted>"

// LoadConfigDocument reads and decodes the config document at source. Empty,
// inline and unreadable sources return ErrNotPersistable.
func LoadConfigDocument(source string) (map[string]interface{}, string, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" || strings.HasPrefix(trimmed, "inline") {
		return nil, "", fmt.Errorf("%w: no writable config path", ErrNotPersistable)
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrNotPersistable, err)
	}
	root, format, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, "", err
	}
	return root, format, nil
}

// ParseConfigDocument parses an editor document (YAML or JSON) into a root map.
func ParseConfigDocument(raw string) (map[string]interface{}, error) {
	root, _, err := decodeConfigDocument([]byte(raw))
	if err != nil {
		return nil, err
	}
	return root, nil
}

// RenderConfigYAML serializes a config document for display in the raw editor.
func RenderConfigYAML(root map[string]interface{}) ([]byte, error) {
	return encodeConfigDocument(root, "yaml")
}

// EncodeConfigDocument serializes a config document in the requested format
// ("json" or "yaml"). It is used to mirror a saved config to a remote backing
// store (e.g. a kubernetes Secret).
func EncodeConfigDocument(root map[string]interface{}, format string) ([]byte, error) {
	return encodeConfigDocument(root, format)
}

// SaveConfigDocument writes root back to source, preserving the original
// document format (JSON vs YAML) and file permissions. The write is atomic.
func SaveConfigDocument(source string, root map[string]interface{}) (string, error) {
	configFileMu.Lock()
	defer configFileMu.Unlock()

	trimmed := strings.TrimSpace(source)
	if trimmed == "" || strings.HasPrefix(trimmed, "inline") {
		return "", fmt.Errorf("%w: no writable config path", ErrNotPersistable)
	}

	original, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotPersistable, err)
	}
	_, format, err := decodeConfigDocument(original)
	if err != nil {
		return "", err
	}

	encoded, err := encodeConfigDocument(root, format)
	if err != nil {
		return "", err
	}
	if err := writeConfigFileAtomic(trimmed, encoded); err != nil {
		return "", err
	}
	return format, nil
}

// BuildRuntimeConfigDocument reconstructs a best-effort config document from the
// effective runtime values and declared aliases. It is used as a fallback when
// no config file is available so the raw view is still populated.
func BuildRuntimeConfigDocument(defs []integrationinterface.Definition, values map[string]runtimecfg.Value) map[string]interface{} {
	type aliasTarget struct {
		name    string
		jsonKey string
	}
	aliasByEnvKey := map[string]aliasTarget{}
	for _, def := range defs {
		name := strings.ToLower(strings.TrimSpace(def.Name))
		for _, alias := range def.RuntimeConfigAliases {
			envKey := normalizeKey(alias.EnvKey)
			jsonKey := strings.TrimSpace(alias.JSONKey)
			if envKey == "" || jsonKey == "" {
				continue
			}
			aliasByEnvKey[envKey] = aliasTarget{name: name, jsonKey: jsonKey}
		}
	}

	env := map[string]interface{}{}
	integrations := map[string]interface{}{}
	for key, value := range values {
		normalized := normalizeKey(key)
		if normalized == "" {
			continue
		}
		if alias, ok := aliasByEnvKey[normalized]; ok {
			section, _ := integrations[alias.name].(map[string]interface{})
			if section == nil {
				section = map[string]interface{}{}
			}
			section[alias.jsonKey] = value.Value
			integrations[alias.name] = section
			continue
		}
		env[normalized] = value.Value
	}

	root := map[string]interface{}{}
	if len(env) > 0 {
		root["env"] = env
	}
	if len(integrations) > 0 {
		root["integrations"] = integrations
	}
	return root
}

// RedactConfig returns a deep copy of root with sensitive env values and
// sensitive integration alias values replaced by RedactedValue.
func RedactConfig(root map[string]interface{}, values map[string]runtimecfg.Value, defs []integrationinterface.Definition) map[string]interface{} {
	out := deepCopyMap(root)
	envSensitive, integrationSensitive := sensitiveLocations(defs, values)

	if env, ok := out["env"].(map[string]interface{}); ok {
		for key := range env {
			if envSensitive[normalizeKey(key)] {
				env[key] = RedactedValue
			}
		}
	}

	if integrations, ok := out["integrations"].(map[string]interface{}); ok {
		for name, sectionRaw := range integrations {
			section, ok := sectionRaw.(map[string]interface{})
			if !ok {
				continue
			}
			sensitive := integrationSensitive[strings.ToLower(strings.TrimSpace(name))]
			if len(sensitive) == 0 {
				continue
			}
			for key := range section {
				if sensitive[normalizeAliasJSONKey(key)] {
					section[key] = RedactedValue
				}
			}
		}
	}

	return out
}

// RestoreRedactedConfig replaces RedactedValue entries in incoming with the
// matching value from original (dropping the key when the original had none).
func RestoreRedactedConfig(original, incoming map[string]interface{}, values map[string]runtimecfg.Value, defs []integrationinterface.Definition) map[string]interface{} {
	out := deepCopyMap(incoming)
	envSensitive, integrationSensitive := sensitiveLocations(defs, values)

	if env, ok := out["env"].(map[string]interface{}); ok {
		originalEnv, _ := original["env"].(map[string]interface{})
		for key, value := range env {
			if !envSensitive[normalizeKey(key)] || value != RedactedValue {
				continue
			}
			if originalValue, ok := originalEnv[key]; ok {
				env[key] = originalValue
			} else {
				delete(env, key)
			}
		}
	}

	if integrations, ok := out["integrations"].(map[string]interface{}); ok {
		originalIntegrations, _ := original["integrations"].(map[string]interface{})
		for name, sectionRaw := range integrations {
			section, ok := sectionRaw.(map[string]interface{})
			if !ok {
				continue
			}
			sensitive := integrationSensitive[strings.ToLower(strings.TrimSpace(name))]
			if len(sensitive) == 0 {
				continue
			}
			originalSection, _ := originalIntegrations[name].(map[string]interface{})
			for key, value := range section {
				if !sensitive[normalizeAliasJSONKey(key)] || value != RedactedValue {
					continue
				}
				if originalValue, ok := originalSection[key]; ok {
					section[key] = originalValue
				} else {
					delete(section, key)
				}
			}
		}
	}

	return out
}

// ValidateConfigDocument checks the raw config document shape: only known
// top-level keys, mapping sections, and integration entries that map to a
// declared runtime env var (direct key, alias or derived OCI_ key).
func ValidateConfigDocument(root map[string]interface{}, defs []integrationinterface.Definition) []string {
	errs := []string{}

	for key := range root {
		switch key {
		case "env", "integrations", "bootstrap", "anchors":
		default:
			errs = append(errs, fmt.Sprintf("unknown top-level key %q (allowed: env, integrations, bootstrap, anchors)", key))
		}
	}

	for _, section := range []string{"env", "integrations", "bootstrap"} {
		raw, ok := root[section]
		if !ok || raw == nil {
			continue
		}
		if _, ok := raw.(map[string]interface{}); !ok {
			errs = append(errs, fmt.Sprintf("%s must be a mapping", section))
		}
	}

	byName := map[string]integrationinterface.Definition{}
	for _, def := range defs {
		byName[strings.ToLower(strings.TrimSpace(def.Name))] = def
	}

	integrationsRaw, ok := root["integrations"].(map[string]interface{})
	if !ok {
		return errs
	}
	for name, sectionRaw := range integrationsRaw {
		def, known := byName[strings.ToLower(strings.TrimSpace(name))]
		if !known {
			errs = append(errs, fmt.Sprintf("integrations.%s: unknown integration", name))
			continue
		}
		if len(def.RuntimeEnvVars) == 0 {
			errs = append(errs, fmt.Sprintf("integrations.%s: integration has no declared runtime env vars", name))
			continue
		}
		section, ok := sectionRaw.(map[string]interface{})
		if !ok {
			if sectionRaw != nil {
				errs = append(errs, fmt.Sprintf("integrations.%s must be a mapping", name))
			}
			continue
		}
		for key := range section {
			if resolveEnvKey(def, key) == "" {
				errs = append(errs, fmt.Sprintf("integrations.%s.%s does not map to a declared runtime env var", name, key))
			}
		}
	}

	return errs
}

// sensitiveLocations returns the sensitive env keys and (per integration) the
// sensitive alias JSON keys derived from runtime sensitivity flags and the
// integration declarations.
func sensitiveLocations(defs []integrationinterface.Definition, values map[string]runtimecfg.Value) (map[string]bool, map[string]map[string]bool) {
	envSensitive := map[string]bool{}
	for key, value := range values {
		if value.Sensitive {
			envSensitive[normalizeKey(key)] = true
		}
	}
	for _, def := range defs {
		for _, decl := range def.RuntimeEnvVars {
			if decl.Sensitive {
				envSensitive[normalizeKey(decl.Key)] = true
			}
		}
	}

	integrationSensitive := map[string]map[string]bool{}
	for _, def := range defs {
		name := strings.ToLower(strings.TrimSpace(def.Name))
		for _, alias := range def.RuntimeConfigAliases {
			envKey := normalizeKey(alias.EnvKey)
			jsonKey := normalizeAliasJSONKey(alias.JSONKey)
			if envKey == "" || jsonKey == "" || !envSensitive[envKey] {
				continue
			}
			if integrationSensitive[name] == nil {
				integrationSensitive[name] = map[string]bool{}
			}
			integrationSensitive[name][jsonKey] = true
		}
	}
	return envSensitive, integrationSensitive
}

// resolveEnvKey mirrors flattenConfigEnv's key resolution: a direct declared env
// key, a declared alias JSON key, or a derived OCI_<INTEGRATION>_<KEY> key.
func resolveEnvKey(def integrationinterface.Definition, key string) string {
	declared := map[string]bool{}
	for _, decl := range def.RuntimeEnvVars {
		declared[normalizeKey(decl.Key)] = true
	}

	direct := normalizeKey(key)
	if declared[direct] {
		return direct
	}
	for _, alias := range def.RuntimeConfigAliases {
		if normalizeAliasJSONKey(alias.JSONKey) == normalizeAliasJSONKey(key) {
			return normalizeKey(alias.EnvKey)
		}
	}
	derived := deriveIntegrationEnvKey(def.Name, key)
	if declared[derived] {
		return derived
	}
	return ""
}

func deriveIntegrationEnvKey(integrationName, key string) string {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(key)), "OCI_") {
		return strings.ToUpper(strings.TrimSpace(key))
	}
	integrationPart := normalizeTokenForEnv(integrationName)
	keyPart := normalizeTokenForEnv(key)
	if integrationPart == "" || keyPart == "" {
		return ""
	}
	return "OCI_" + integrationPart + "_" + keyPart
}

func normalizeTokenForEnv(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	b := strings.Builder{}
	lastUnderscore := false
	for _, r := range upper {
		isAlpha := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func deepCopyMap(root map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(root))
	for key, value := range root {
		out[key] = deepCopyValue(value)
	}
	return out
}

func deepCopyValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return deepCopyMap(typed)
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = deepCopyValue(item)
		}
		return out
	default:
		return value
	}
}
