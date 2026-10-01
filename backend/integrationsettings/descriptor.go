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
	UserVisible  bool     `json:"user_visible"`
	Group        string   `json:"group,omitempty"`
	Order        int      `json:"order"`
	Placeholder  string   `json:"placeholder,omitempty"`
	Default      string   `json:"default,omitempty"`
	Options      []string `json:"options,omitempty"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	Step         *float64 `json:"step,omitempty"`
	Value        string   `json:"value"`
	Configured   bool     `json:"configured"`
	ConfigTarget string   `json:"config_target"`
}

// validFieldType reports whether the given explicit type hint is supported by
// the settings renderer.
func validFieldType(fieldType string) bool {
	switch strings.ToLower(strings.TrimSpace(fieldType)) {
	case FieldTypeString, FieldTypeBool, FieldTypeNumber, FieldTypeSelect, FieldTypeJSON, FieldTypeSecret:
		return true
	default:
		return false
	}
}

// humanizeKey turns an env key such as "OCI_MSGMATE_TOKENS_PER_EUR" into a
// readable "Msgmate tokens per eur" fallback label.
func humanizeKey(key string) string {
	trimmed := strings.TrimSpace(key)
	trimmed = strings.TrimPrefix(trimmed, "OCI_")
	parts := strings.Split(trimmed, "_")
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		words = append(words, strings.ToLower(part))
	}
	if len(words) == 0 {
		return trimmed
	}
	label := strings.Join(words, " ")
	return strings.ToUpper(label[:1]) + label[1:]
}

func normalizeKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}

func normalizeAliasJSONKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// InferFieldType derives the renderer type for a declaration. An explicit,
// supported declaration Type wins; otherwise the type is inferred from
// Sensitive/Key/value.
func InferFieldType(decl integrationinterface.RuntimeEnvVar, current string) string {
	if decl.Sensitive {
		return FieldTypeSecret
	}
	if validFieldType(decl.Type) {
		return strings.ToLower(strings.TrimSpace(decl.Type))
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
	if looksLikeNumberKey(key) || looksLikeNumberValue(current) {
		return FieldTypeNumber
	}
	return FieldTypeString
}

// looksLikeNumberKey is a conservative heuristic that classifies obviously
// numeric env keys as numbers when no explicit type is declared.
func looksLikeNumberKey(key string) bool {
	suffixes := []string{
		"_RATE",
		"_PRICE",
		"_COUNT",
		"_LIMIT",
		"_PER_",
		"_TOKENS",
		"_DAYS",
		"_SECONDS",
		"_TIMEOUT",
		"_SIZE",
	}
	for _, suffix := range suffixes {
		if strings.Contains(key, suffix) {
			return true
		}
	}
	return false
}

// looksLikeNumberValue reports whether an unset/default value is a plain
// number. It only applies to single-line values.
func looksLikeNumberValue(current string) bool {
	trimmed := strings.TrimSpace(current)
	if trimmed == "" || strings.Contains(trimmed, "\n") {
		return false
	}
	_, err := strconv.ParseFloat(trimmed, 64)
	return err == nil
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
	if def.Name == CoreSettingsName {
		if section, ok := coreBootstrapSectionForKey(key); ok {
			return "bootstrap." + section
		}
	}
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

// descriptorGroup assigns UI grouping for synthetic definitions. Core splits
// its fields into an Environment and a Bootstrap section.
func descriptorGroup(def integrationinterface.Definition, key string) string {
	if def.Name != CoreSettingsName {
		return ""
	}
	if isCoreBootstrapKey(key) {
		return "Bootstrap"
	}
	return "Environment"
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

		advanced := decl.Advanced || (fieldType == FieldTypeJSON && strings.Contains(current, "\n"))

		label := strings.TrimSpace(decl.Label)
		if label == "" {
			label = humanizeKey(key)
		}
		group := strings.TrimSpace(decl.Group)
		if group == "" {
			group = descriptorGroup(def, key)
		}

		descriptors = append(descriptors, FieldDescriptor{
			Key:          key,
			Label:        label,
			Type:         fieldType,
			Description:  strings.TrimSpace(decl.Description),
			Sensitive:    sensitive,
			Required:     decl.Required,
			Advanced:     advanced,
			UserVisible:  decl.UserVisible,
			Group:        group,
			Order:        decl.Order,
			Placeholder:  strings.TrimSpace(decl.Placeholder),
			Default:      strings.TrimSpace(decl.Default),
			Options:      append([]string(nil), decl.Options...),
			Min:          decl.Min,
			Max:          decl.Max,
			Step:         decl.Step,
			Value:        displayValue,
			Configured:   configured,
			ConfigTarget: resolveConfigTarget(def, key),
		})
	}
	return descriptors
}
