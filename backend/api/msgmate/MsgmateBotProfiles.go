package msgmate

import (
	"backend/database"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

// BotProfileConfig represents the configuration for a bot model
type BotProfileConfig struct {
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"max_tokens"`
	Tools        []string `json:"tools,omitempty"`
	Model        string   `json:"model"`
	Endpoint     string   `json:"endpoint"`
	Backend      string   `json:"backend"`
	Context      int      `json:"context"`
	SystemPrompt string   `json:"system_prompt"`
	Reasoning    *bool    `json:"reasoning,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// BotModel represents a bot model configuration
type BotModel struct {
	Title         string           `json:"title"`
	Description   string           `json:"description"`
	Configuration BotProfileConfig `json:"configuration"`
}

// GetBotModels returns bot model configurations from the database.
func GetBotModels(DB *gorm.DB) ([]BotModel, error) {
	var configs []database.ModelConfig
	if err := DB.Order("id ASC").Find(&configs).Error; err != nil {
		return nil, err
	}

	models := make([]BotModel, 0, len(configs))
	for _, cfg := range configs {
		var profileConfig BotProfileConfig
		if err := json.Unmarshal(cfg.Configuration, &profileConfig); err != nil {
			return nil, fmt.Errorf("failed to parse configuration for model %q: %w", cfg.ModelID, err)
		}

		models = append(models, BotModel{
			Title:         cfg.Title,
			Description:   cfg.Description,
			Configuration: profileConfig,
		})
	}

	return models, nil
}

// HasTag checks if a configuration has a specific tag
func (config *BotProfileConfig) HasTag(tag string) bool {
	for _, t := range config.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// HasSkipCoreTag checks if the configuration has the "skip-core" tag
func (config *BotProfileConfig) HasSkipCoreTag() bool {
	return config.HasTag("skip-core")
}

// CreateOrUpdateBotProfile creates or updates the bot profile in the database
func CreateOrUpdateBotProfile(DB *gorm.DB, botUser database.User) error {
	// first check if the profile exists
	var botProfile database.PublicProfile
	DB.Where("user_id = ?", botUser.ID).First(&botProfile)
	if botProfile.ID != 0 {
		// Hard delete the old profile
		DB.Unscoped().Delete(&botProfile)
	}

	models, err := GetBotModels(DB)
	if err != nil {
		return err
	}

	// Convert to interface{} slice for JSON marshaling
	modelsInterface := make([]interface{}, len(models))
	for i, model := range models {
		modelsInterface[i] = model
	}

	// Create profile data and new profile instance
	botProfileInfo := map[string]interface{}{
		"name":        "Bot",
		"description": "This is a bot user",
		"models":      modelsInterface,
	}

	botProfileBytes, err := json.Marshal(botProfileInfo)
	if err != nil {
		return err
	}

	// Create a new profile instance
	newBotProfile := database.PublicProfile{
		ProfileData: botProfileBytes,
		UserId:      botUser.ID,
	}

	q := DB.Create(&newBotProfile)
	if q.Error != nil {
		return q.Error
	}

	return nil
}
