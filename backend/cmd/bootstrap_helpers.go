package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func resolveBootstrapSpecBytes(spec string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return []byte(trimmed), "inline", nil
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, "", fmt.Errorf("failed reading bootstrap spec file %q: %w", trimmed, err)
	}
	return raw, trimmed, nil
}

func decodeOneOrManyJSON[T any](raw []byte, source string) ([]T, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []T{}, nil
	}

	if trimmed[0] == '[' {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		var out []T
		if err := decoder.Decode(&out); err != nil {
			return nil, fmt.Errorf("invalid JSON array in %s: %w", source, err)
		}
		var trailing interface{}
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("invalid JSON array in %s: unexpected trailing JSON", source)
			}
			return nil, fmt.Errorf("invalid JSON array in %s: %w", source, err)
		}
		return out, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var single T
	if err := decoder.Decode(&single); err != nil {
		return nil, fmt.Errorf("invalid JSON object in %s: %w", source, err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("invalid JSON object in %s: unexpected trailing JSON", source)
		}
		return nil, fmt.Errorf("invalid JSON object in %s: %w", source, err)
	}
	return []T{single}, nil
}

func normalizeOwnersList(owners []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(owners))
	for _, owner := range owners {
		trimmed := strings.TrimSpace(owner)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
