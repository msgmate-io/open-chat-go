package database

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"slices"

	"gorm.io/gorm"
)

//go:embed data/default_model_configs.json
var defaultModelConfigsJSON []byte

// ModelConfig stores a default LLM model definition that can be assigned to bot profiles.
type ModelConfig struct {
	Model
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	ModelID       string          `json:"model_id" gorm:"uniqueIndex;not null"`
	Configuration json.RawMessage `json:"configuration" gorm:"type:jsonb"`
	BotUsernames  []string        `json:"bot_usernames" gorm:"serializer:json"`
	IsDefault     bool            `json:"is_default" gorm:"default:false"`
}

// AssignedToBot reports whether this model is configured for the given bot username.
func (m ModelConfig) AssignedToBot(username string) bool {
	return slices.Contains(m.BotUsernames, username)
}

type modelConfigFileEntry struct {
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	BotUsernames  []string        `json:"bot_usernames"`
	Configuration json.RawMessage `json:"configuration"`
}

type modelConfigID struct {
	Model string `json:"model"`
}

// AssignBotToModelConfig adds a bot username to a model config assignment.
// Returns true when the assignment was newly added.
func AssignBotToModelConfig(db *gorm.DB, modelID, botUsername string) (bool, error) {
	var cfg ModelConfig
	if err := db.Where("model_id = ?", modelID).First(&cfg).Error; err != nil {
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
func UnassignBotFromModelConfig(db *gorm.DB, modelID, botUsername string) (bool, error) {
	var cfg ModelConfig
	if err := db.Where("model_id = ?", modelID).First(&cfg).Error; err != nil {
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

// SeedModelConfigs loads default model definitions from the embedded JSON file
// and inserts any that are not already present (matched by model_id).
func SeedModelConfigs(db *gorm.DB) error {
	var entries []modelConfigFileEntry
	if err := json.Unmarshal(defaultModelConfigsJSON, &entries); err != nil {
		return fmt.Errorf("failed to parse default model configs: %w", err)
	}

	for _, entry := range entries {
		var cfg modelConfigID
		if err := json.Unmarshal(entry.Configuration, &cfg); err != nil {
			return fmt.Errorf("failed to parse configuration for %q: %w", entry.Title, err)
		}
		if cfg.Model == "" {
			return fmt.Errorf("model config %q is missing configuration.model", entry.Title)
		}

		var existing ModelConfig
		result := db.Where("model_id = ?", cfg.Model).First(&existing)
		if result.Error == nil {
			if !existing.IsDefault {
				if err := db.Model(&existing).Update("is_default", true).Error; err != nil {
					return fmt.Errorf("failed to mark model config %q as default: %w", cfg.Model, err)
				}
			}
			continue
		}
		if result.Error != gorm.ErrRecordNotFound {
			return result.Error
		}

		record := ModelConfig{
			Title:         entry.Title,
			Description:   entry.Description,
			ModelID:       cfg.Model,
			Configuration: entry.Configuration,
			BotUsernames:  entry.BotUsernames,
			IsDefault:     true,
		}
		if err := db.Create(&record).Error; err != nil {
			return fmt.Errorf("failed to seed model config %q: %w", cfg.Model, err)
		}
		log.Printf("Seeded model config: %s (bots: %v)", cfg.Model, entry.BotUsernames)
	}

	return nil
}
