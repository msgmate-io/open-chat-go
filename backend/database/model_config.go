package database

import (
	"backend/runtimecfg"
	"bytes"
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"gorm.io/gorm"
)

//go:embed data/default_model_configs.json
var defaultModelConfigsJSON []byte

//go:embed data/extra_default_models_config.json
var extraDefaultModelsConfigJSON []byte

// ModelConfig stores a default LLM model definition that can be assigned to bot profiles.
type ModelConfig struct {
	Model
	OwnerUserId   *uint           `json:"owner_user_id" gorm:"index"`
	OwnerUser     *User           `json:"-" gorm:"foreignKey:OwnerUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	ModelID       string          `json:"model_id" gorm:"index;not null"`
	Configuration json.RawMessage `json:"configuration" gorm:"type:jsonb"`
	BotUsernames  StringSliceJSON `json:"bot_usernames" gorm:"type:jsonb"`
	IsPublic      bool            `json:"is_public" gorm:"default:false;index"`
	IsDefault     bool            `json:"is_default" gorm:"default:false"`
}

type StringSliceJSON []string

func (s *StringSliceJSON) Scan(value interface{}) error {
	if value == nil {
		*s = StringSliceJSON{}
		return nil
	}

	var raw string
	switch v := value.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		return fmt.Errorf("unsupported type for StringSliceJSON: %T", value)
	}

	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		*s = StringSliceJSON{}
		return nil
	}

	if strings.HasPrefix(raw, "[") {
		var parsed []string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return err
		}
		*s = StringSliceJSON(parsed)
		return nil
	}

	raw = strings.Trim(raw, `"`)
	if raw == "" {
		*s = StringSliceJSON{}
		return nil
	}
	*s = StringSliceJSON{raw}
	return nil
}

func (s StringSliceJSON) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// AssignedToBot reports whether this model is configured for the given bot username.
func (m ModelConfig) AssignedToBot(username string) bool {
	return slices.Contains(m.BotUsernames, username)
}

type modelConfigFileEntry struct {
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	BotUsernames  []string        `json:"bot_usernames"`
	IsPublic      *bool           `json:"is_public"`
	Configuration json.RawMessage `json:"configuration"`
}

type modelConfigID struct {
	Model string `json:"model"`
}

// AssignBotToModelConfig adds a bot username to a model config assignment.
// Returns true when the assignment was newly added.
func AssignBotToModelConfig(db *gorm.DB, modelUUID, botUsername string) (bool, error) {
	var cfg ModelConfig
	if err := db.Where("uuid = ?", modelUUID).First(&cfg).Error; err != nil {
		return false, err
	}
	if cfg.AssignedToBot(botUsername) {
		return false, nil
	}
	cfg.BotUsernames = append(cfg.BotUsernames, botUsername)
	if err := db.Model(&cfg).Update("bot_usernames", cfg.BotUsernames).Error; err != nil {
		return false, err
	}
	return true, nil
}

// UnassignBotFromModelConfig removes a bot username from a model config assignment.
// Returns true when the assignment was removed.
func UnassignBotFromModelConfig(db *gorm.DB, modelUUID, botUsername string) (bool, error) {
	var cfg ModelConfig
	if err := db.Where("uuid = ?", modelUUID).First(&cfg).Error; err != nil {
		return false, err
	}
	if !cfg.AssignedToBot(botUsername) {
		return false, nil
	}
	updated := slices.DeleteFunc(cfg.BotUsernames, func(username string) bool {
		return username == botUsername
	})
	if err := db.Model(&cfg).Update("bot_usernames", updated).Error; err != nil {
		return false, err
	}
	return true, nil
}

// GetModelConfigsForBot returns model configs assigned to the given bot username.
func GetModelConfigsForBot(db *gorm.DB, botUsername string) ([]ModelConfig, error) {
	var configs []ModelConfig
	if err := db.Order("id ASC").Find(&configs).Error; err != nil {
		return nil, err
	}

	matched := make([]ModelConfig, 0, len(configs))
	for _, cfg := range configs {
		if cfg.AssignedToBot(botUsername) {
			matched = append(matched, cfg)
		}
	}
	return matched, nil
}

func runtimeConfigValue(key string) string {
	values := runtimecfg.GetAll()
	if value, ok := values[key]; ok {
		return strings.TrimSpace(value.Value)
	}
	return strings.TrimSpace(os.Getenv(key))
}

func managedProviderAPIKeyEnv(backend string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "openai":
		return "OPENAI_API_KEY", true
	case "anthropic":
		return "ANTHROPIC_API_KEY", true
	case "deepinfra":
		return "DEEPINFRA_API_KEY", true
	case "groq":
		return "GROQ_API_KEY", true
	case "litellm":
		return "LITELLM_API_KEY", true
	case "msgmate_cluster":
		return "MSGMATE_CLUSTER_API_KEY", true
	default:
		return "", false
	}
}

func modelConfigBackend(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	cfg := map[string]interface{}{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	backend, _ := cfg["backend"].(string)
	return strings.ToLower(strings.TrimSpace(backend)), nil
}

// DefaultBotProviderKeySyncResult reports assignment changes applied while
// syncing default bot model access from provider API key availability.
type DefaultBotProviderKeySyncResult struct {
	Assigned         int
	Unassigned       int
	SkippedUnmanaged int
	SkippedInvalid   int
}

// SyncDefaultBotModelsByProviderKeys enables/disables default-model assignments
// for the given bot username based on provider API key availability.
//
// Only provider backends with explicit key mappings are managed. Models with
// unknown backends or invalid configuration are left untouched.
func SyncDefaultBotModelsByProviderKeys(db *gorm.DB, botUsername string) (DefaultBotProviderKeySyncResult, error) {
	result := DefaultBotProviderKeySyncResult{}
	botUsername = strings.TrimSpace(botUsername)
	if botUsername == "" {
		return result, fmt.Errorf("bot username is required")
	}

	rows := []ModelConfig{}
	if err := db.Where("owner_user_id IS NULL AND is_default = ?", true).Find(&rows).Error; err != nil {
		return result, err
	}

	for _, row := range rows {
		backend, err := modelConfigBackend(row.Configuration)
		if err != nil {
			result.SkippedInvalid++
			continue
		}

		apiKeyEnv, managed := managedProviderAPIKeyEnv(backend)
		if !managed {
			result.SkippedUnmanaged++
			continue
		}

		hasAPIKey := strings.TrimSpace(runtimeConfigValue(apiKeyEnv)) != ""
		isAssigned := row.AssignedToBot(botUsername)

		if hasAPIKey && !isAssigned {
			updated := append(append(StringSliceJSON{}, row.BotUsernames...), botUsername)
			if err := db.Model(&row).Update("bot_usernames", updated).Error; err != nil {
				return result, err
			}
			result.Assigned++
			continue
		}

		if !hasAPIKey && isAssigned {
			updated := slices.DeleteFunc(row.BotUsernames, func(username string) bool {
				return username == botUsername
			})
			if err := db.Model(&row).Update("bot_usernames", updated).Error; err != nil {
				return result, err
			}
			result.Unassigned++
		}
	}

	return result, nil
}

// ResolveExtraModelConfigsJSON returns the extra default-models JSON payload used
// at seed time. EXTRA_MODELS_JSON / --extra-models-json may be a filesystem path
// or an inline JSON array; otherwise the embedded extra_default_models_config.json
// is used.
func ResolveExtraModelConfigsJSON() ([]byte, string, error) {
	override := runtimeConfigValue("EXTRA_MODELS_JSON")
	if override == "" {
		return extraDefaultModelsConfigJSON, "embedded:extra_default_models_config.json", nil
	}

	trimmed := strings.TrimSpace(override)
	if strings.HasPrefix(trimmed, "[") {
		return []byte(trimmed), "EXTRA_MODELS_JSON (inline)", nil
	}

	content, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, "", fmt.Errorf("failed reading EXTRA_MODELS_JSON path %q: %w", trimmed, err)
	}
	return content, trimmed, nil
}

func parseModelConfigFileEntries(raw []byte, source string) ([]modelConfigFileEntry, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []modelConfigFileEntry{}, nil
	}
	var entries []modelConfigFileEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", source, err)
	}
	for idx, entry := range entries {
		var cfg modelConfigID
		if err := json.Unmarshal(entry.Configuration, &cfg); err != nil {
			return nil, fmt.Errorf("%s[%d] has invalid configuration JSON: %w", source, idx, err)
		}
		if strings.TrimSpace(cfg.Model) == "" {
			return nil, fmt.Errorf("%s[%d] is missing configuration.model", source, idx)
		}
	}
	return entries, nil
}

func seedModelConfigEntries(db *gorm.DB, entries []modelConfigFileEntry, source string) error {
	for _, entry := range entries {
		var cfg modelConfigID
		if err := json.Unmarshal(entry.Configuration, &cfg); err != nil {
			return fmt.Errorf("failed to parse configuration for %q from %s: %w", entry.Title, source, err)
		}
		if cfg.Model == "" {
			return fmt.Errorf("model config %q from %s is missing configuration.model", entry.Title, source)
		}

		var existing ModelConfig
		result := db.Where("owner_user_id IS NULL AND model_id = ?", cfg.Model).First(&existing)
		if result.Error == nil {
			updates := map[string]interface{}{}
			if !existing.IsDefault {
				updates["is_default"] = true
			}
			if !existing.IsPublic {
				updates["is_public"] = true
			}
			if len(updates) > 0 {
				if err := db.Model(&existing).Updates(updates).Error; err != nil {
					return fmt.Errorf("failed to update seeded model config %q: %w", cfg.Model, err)
				}
			}
			continue
		}
		if result.Error != gorm.ErrRecordNotFound {
			return result.Error
		}

		isPublic := true
		if entry.IsPublic != nil {
			isPublic = *entry.IsPublic
		}

		record := ModelConfig{
			Title:         entry.Title,
			Description:   entry.Description,
			ModelID:       cfg.Model,
			Configuration: entry.Configuration,
			BotUsernames:  entry.BotUsernames,
			IsPublic:      isPublic,
			IsDefault:     true,
		}
		if err := db.Create(&record).Error; err != nil {
			return fmt.Errorf("failed to seed model config %q from %s: %w", cfg.Model, source, err)
		}
		log.Printf("Seeded model config: %s (bots: %v, source: %s)", cfg.Model, entry.BotUsernames, source)
	}

	return nil
}

// SeedModelConfigs loads default model definitions from the embedded JSON file
// plus optional EXTRA_MODELS_JSON / embedded extras, and inserts any that are
// not already present (matched by model_id).
func SeedModelConfigs(db *gorm.DB) error {
	defaultEntries, err := parseModelConfigFileEntries(defaultModelConfigsJSON, "default_model_configs.json")
	if err != nil {
		return err
	}
	if err := seedModelConfigEntries(db, defaultEntries, "default_model_configs.json"); err != nil {
		return err
	}

	extraRaw, extraSource, err := ResolveExtraModelConfigsJSON()
	if err != nil {
		return err
	}
	extraEntries, err := parseModelConfigFileEntries(extraRaw, extraSource)
	if err != nil {
		return err
	}
	return seedModelConfigEntries(db, extraEntries, extraSource)
}
