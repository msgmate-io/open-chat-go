package main

import (
	"backend/api/msgmate"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"unicode"
)

type toolManifestEntry struct {
	Name                 string                 `json:"name"`
	CallTypeName         string                 `json:"call_type_name"`
	InitTypeName         string                 `json:"init_type_name,omitempty"`
	CallSchema           map[string]interface{} `json:"call_schema"`
	InitSchema           map[string]interface{} `json:"init_schema,omitempty"`
	RequiresInit         bool                   `json:"requires_init"`
	RequiresConfirmation bool                   `json:"requires_confirmation"`
	AdminOnly            bool                   `json:"admin_only"`
}

func toPascalIdentifier(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "Tool"
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return !(unicode.IsDigit(r) || unicode.IsLetter(r))
	})
	if len(parts) == 0 {
		return "Tool"
	}

	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	name := b.String()
	if name == "" {
		return "Tool"
	}
	if unicode.IsDigit([]rune(name)[0]) {
		return "Tool" + name
	}
	return name
}

func cloneSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

func addTitle(schema map[string]interface{}, title string) map[string]interface{} {
	resolved := cloneSchema(schema)
	if resolved == nil {
		resolved = map[string]interface{}{"type": "object"}
	}
	if existing, ok := resolved["title"].(string); ok && strings.TrimSpace(existing) != "" {
		return resolved
	}
	resolved["title"] = title
	return resolved
}

func main() {
	entries := make([]toolManifestEntry, 0, len(msgmate.AllTools))
	for _, tool := range msgmate.AllTools {
		base := toPascalIdentifier(tool.GetToolName())
		callType := base + "Call"
		entry := toolManifestEntry{
			Name:                 tool.GetToolName(),
			CallTypeName:         callType,
			CallSchema:           addTitle(tool.GetToolInputSchema(), callType),
			RequiresInit:         tool.GetRequiresInit(),
			RequiresConfirmation: tool.GetRequiresConfirmation(),
			AdminOnly:            tool.GetAdminOnly(),
		}
		if tool.GetRequiresInit() {
			initType := base + "Init"
			entry.InitTypeName = initType
			initSchema := tool.GetToolInitSchema()
			if initSchema == nil {
				initSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
					"required":   []string{},
				}
			}
			entry.InitSchema = addTitle(initSchema, initType)
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]interface{}{"rows": entries})
}
