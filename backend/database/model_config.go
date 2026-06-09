package database

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"

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
	IsDefault     bool            `json:"is_default" gorm:"default:false"`
}

type modelConfigFileEntry struct {
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Configuration json.RawMessage `json:"configuration"`
}

type modelConfigID struct {
	Model string `json:"model"`
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
			IsDefault:     true,
		}
		if err := db.Create(&record).Error; err != nil {
			return fmt.Errorf("failed to seed model config %q: %w", cfg.Model, err)
		}
		log.Printf("Seeded model config: %s", cfg.Model)
	}

	return nil
}
