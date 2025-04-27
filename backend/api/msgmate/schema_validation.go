package msgmate

import (
	"fmt"
	"strings"
)

func ValidatePayloadAgainstSchema(payload map[string]interface{}, schema map[string]interface{}, strictUnknown bool) error {
	if schema == nil {
		return nil
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}

	if schemaType, _ := schema["type"].(string); schemaType != "" && schemaType != "object" {
		return fmt.Errorf("unsupported top-level schema type %q", schemaType)
	}

	requiredFields := map[string]struct{}{}
	switch requiredRaw := schema["required"].(type) {
	case []string:
		for _, field := range requiredRaw {
			if strings.TrimSpace(field) != "" {
				requiredFields[field] = struct{}{}
			}
		}
	case []interface{}:
		for _, field := range requiredRaw {
			if name, ok := field.(string); ok && strings.TrimSpace(name) != "" {
				requiredFields[name] = struct{}{}
			}
		}
	}

	properties, _ := schema["properties"].(map[string]interface{})
	for field := range requiredFields {
		if _, exists := payload[field]; !exists {
			return fmt.Errorf("missing required field %q", field)
		}
	}

	for field, value := range payload {
		prop, known := properties[field]
		if !known {
			if strictUnknown {
				return fmt.Errorf("field %q is not allowed", field)
			}
			if allowAdditional, ok := schema["additionalProperties"].(bool); ok && !allowAdditional {
				return fmt.Errorf("field %q is not allowed", field)
			}
			continue
		}

		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}
		if enumValues, hasEnum := propMap["enum"].([]interface{}); hasEnum && len(enumValues) > 0 {
			matched := false
			for _, allowed := range enumValues {
				if fmt.Sprintf("%v", allowed) == fmt.Sprintf("%v", value) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("field %q has invalid enum value", field)
			}
		}
		if expectedType, ok := propMap["type"].(string); ok && !validateSchemaType(value, expectedType) {
			return fmt.Errorf("field %q has invalid type", field)
		}
	}

	return nil
}

func validateSchemaType(value interface{}, schemaType string) bool {
	switch schemaType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		switch value.(type) {
		case float64, float32, int, int32, int64, uint, uint32, uint64:
			return true
		default:
			return false
		}
	case "integer":
		f, ok := value.(float64)
		if !ok {
			return false
		}
		return float64(int64(f)) == f
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	default:
		return true
	}
}
