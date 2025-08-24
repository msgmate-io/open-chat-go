package integrationsettings

import (
	"strconv"
	"strings"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// FieldType values understood by the settings UI renderer.
const (
	FieldTypeString = "string"
	FieldTypeBool   = "bool"
	FieldTypeNumber = "number"
	FieldTypeSelect = "select"
	FieldTypeJSON   = "json"
	FieldTypeSecret = "secret"
)

// FieldDescriptor is the UI-facing description of one declared runtime env var.
type FieldDescriptor struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Type         string   `json:"type"`
	Description  string   `json:"description,omitempty"`
	Sensitive    bool     `json:"sensitive"`
	Required     bool     `json:"required"`
	Advanced     bool     `json:"advanced"`
	Group        string   `json:"group,omitempty"`
	Order        int      `json:"order"`
	Placeholder  string   `json:"placeholder,omitempty"`
	Default      string   `json:"default,omitempty"`
	Options      []string `json:"options,omitempty"`
	Value        string   `json:"value"`
	Configured   bool     `json:"configured"`
	ConfigTarget string   `json:"config_target"`
}

func normalizeKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}

func normalizeAliasJSONKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// InferFieldType derives the renderer type for a declaration.
func InferFieldType(decl integrationinterface.RuntimeEnvVar, current string) string {
	if decl.Sensitive {
		return FieldTypeSecret
	}
	key := normalizeKey(decl.Key)
	if strings.Contains(key, "ENABLE") || strings.HasSuffix(key, "_ENABLED") {
		return FieldTypeBool
	}
	if trimmed := strings.TrimSpace(current); trimmed != "" && !strings.Contains(trimmed, "\n") {
		if _, err := strconv.ParseBool(trimmed); err == nil {
			return FieldTypeBool
		}
	}
	if looksLikeJSONKey(key) || strings.Contains(current, "\n") {
		return FieldTypeJSON
	}
	return FieldTypeString
}

func looksLikeJSONKey(key string) bool {
	suffixes := []string{
		"_JSON",
		"_SPEC",
		"_YAML",
		"BOOTSTRAP",
		"KUBECONFIG",
		"PRIVATE_KEY",
	}
	for _, suffix := range suffixes {
		if strings.Contains(key, suffix) {
			return true
		}
	}
	return false
}

// resolveConfigTarget returns the canonical config write location for a field.
// When a matching RuntimeConfigAlias exists the alias JSON key under
// integrations.<name> is used, otherwise the value is written to env.<KEY>.
func resolveConfigTarget(def integrationinterface.Definition, key string) string {
	normalizedKey := normalizeKey(key)
	for _, alias := range def.RuntimeConfigAliases {
		if normalizeKey(alias.EnvKey) != normalizedKey {
			continue
		}
		jsonKey := strings.TrimSpace(alias.JSONKey)
		if jsonKey == "" {
			continue
		}
		return "integrations." + def.Name + "." + jsonKey
	}
	return "env." + normalizedKey
}

// BuildDescriptors converts a definition's declared RuntimeEnvVars into
// UI-facing field descriptors. When reveal is false, sensitive values are
// masked.
func BuildDescriptors(def integrationinterface.Definition, values map[string]runtimecfg.Value, reveal bool) []FieldDescriptor {
	descriptors := make([]FieldDescriptor, 0, len(def.RuntimeEnvVars))
	for _, decl := range def.RuntimeEnvVars {
		key := normalizeKey(decl.Key)
		if key == "" {
			continue
		}
		current := ""
		sensitive := decl.Sensitive
		if value, ok := values[key]; ok {
			current = value.Value
			sensitive = sensitive || value.Sensitive
		}

		fieldType := InferFieldType(decl, current)
		if sensitive {
			fieldType = FieldTypeSecret
		}

		configured := strings.TrimSpace(current) != ""
		displayValue := current
		if sensitive && !reveal && configured {
			displayValue = "(hidden)"
		}

		advanced := fieldType == FieldTypeJSON && strings.Contains(current, "\n")

		descriptors = append(descriptors, FieldDescriptor{
			Key:          key,
			Label:        key,
			Type:         fieldType,
			Description:  strings.TrimSpace(decl.Description),
			Sensitive:    sensitive,
			Advanced:     advanced,
			Value:        displayValue,
			Configured:   configured,
			ConfigTarget: resolveConfigTarget(def, key),
		})
	}
	return descriptors
}
