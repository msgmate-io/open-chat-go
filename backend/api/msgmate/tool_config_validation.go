package msgmate

import (
	"fmt"
	"strings"
)

type ToolResolver func(toolName string) (Tool, bool, error)

func NormalizeConfiguredToolName(toolName string) string {
	trimmed := strings.TrimSpace(toolName)
	if !strings.Contains(trimmed, ":") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "interaction_start:") || strings.HasPrefix(trimmed, "interaction_complete:") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return trimmed
		}
		return strings.TrimSpace(parts[1])
	}
	return trimmed
}

func ValidateToolsAndInitConfig(toolsRaw interface{}, toolInitRaw interface{}) error {
	return ValidateToolsAndInitConfigWithResolver(toolsRaw, toolInitRaw, nil)
}

func ValidateToolsAndInitConfigWithResolver(toolsRaw interface{}, toolInitRaw interface{}, resolver ToolResolver) error {
	toolNames, err := parseToolNames(toolsRaw)
	if err != nil {
		return err
	}
	toolInitMap, err := parseToolInitMap(toolInitRaw)
	if err != nil {
		return err
	}

	knownToolInitKeys := map[string]struct{}{}
	for idx, configuredName := range toolNames {
		actualName := NormalizeConfiguredToolName(configuredName)
		tool, found, err := resolveToolByName(actualName, resolver)
		if err != nil {
			return fmt.Errorf("tools[%d] lookup failed for %q: %w", idx, configuredName, err)
		}
		if !found || tool == nil {
			return fmt.Errorf("tools[%d] references unknown tool %q", idx, configuredName)
		}

		knownToolInitKeys[configuredName] = struct{}{}
		knownToolInitKeys[actualName] = struct{}{}

		if !tool.GetRequiresInit() {
			continue
		}

		initPayload, has := toolInitMap[configuredName]
		if !has {
			initPayload, has = toolInitMap[actualName]
		}
		if !has {
			return fmt.Errorf("missing tool_init for required tool %q", configuredName)
		}
		if err := ValidatePayloadAgainstSchema(initPayload, tool.GetToolInitSchema(), true); err != nil {
			return fmt.Errorf("tool_init for %q is invalid: %w", configuredName, err)
		}
	}

	for key := range toolInitMap {
		if _, ok := knownToolInitKeys[key]; !ok {
			return fmt.Errorf("tool_init contains unknown tool key %q", key)
		}
	}

	return nil
}

func resolveToolByName(toolName string, resolver ToolResolver) (Tool, bool, error) {
	if tool, found := NewToolByName(toolName); found && tool != nil {
		return tool, true, nil
	}
	if resolver == nil {
		return nil, false, nil
	}
	tool, found, err := resolver(toolName)
	if err != nil {
		return nil, false, err
	}
	return tool, found, nil
}

func parseToolNames(toolsRaw interface{}) ([]string, error) {
	if toolsRaw == nil {
		return []string{}, nil
	}
	switch typed := toolsRaw.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for idx, v := range typed {
			name := strings.TrimSpace(v)
			if name == "" {
				return nil, fmt.Errorf("tools[%d] must be a non-empty string", idx)
			}
			out = append(out, name)
		}
		return out, nil
	case []interface{}:
		out := make([]string, 0, len(typed))
		for idx, raw := range typed {
			v, ok := raw.(string)
			if !ok || strings.TrimSpace(v) == "" {
				return nil, fmt.Errorf("tools[%d] must be a non-empty string", idx)
			}
			out = append(out, strings.TrimSpace(v))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tools must be an array of strings")
	}
}

func parseToolInitMap(toolInitRaw interface{}) (map[string]map[string]interface{}, error) {
	if toolInitRaw == nil {
		return map[string]map[string]interface{}{}, nil
	}
	rawMap, ok := toolInitRaw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("tool_init must be an object")
	}
	out := make(map[string]map[string]interface{}, len(rawMap))
	for key, raw := range rawMap {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			return nil, fmt.Errorf("tool_init keys must be non-empty")
		}
		payload, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("tool_init[%q] must be an object", key)
		}
		out[normalizedKey] = payload
	}
	return out, nil
}
