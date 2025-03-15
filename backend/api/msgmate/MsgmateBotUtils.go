package msgmate

// mapGetOrDefault is a generic utility function to get a value from a map with a default
func mapGetOrDefault[T any](m map[string]interface{}, key string, defaultValue T) T {
	if m == nil {
		return defaultValue
	}

	if val, exists := m[key]; exists {
		// Try direct type conversion first
		if converted, ok := val.(T); ok {
			return converted
		}
		// Special handling for slices/arrays
		switch any(defaultValue).(type) {
		case []string:
			if slice, ok := val.([]interface{}); ok {
				result := make([]string, len(slice))
				for i, item := range slice {
					if str, ok := item.(string); ok {
						result[i] = str
					}
				}
				return any(result).(T)
			}
		}
	}

	return defaultValue
}
