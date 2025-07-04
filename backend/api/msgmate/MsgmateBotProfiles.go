package msgmate

import (
	"backend/chatstate"
	"backend/database"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// BotProfileConfig represents the configuration for a bot model
type BotProfileConfig struct {
	Temperature  float64                `json:"temperature"`
	MaxTokens    int                    `json:"max_tokens"`
	Tools        []string               `json:"tools,omitempty"`
	Integrations []string               `json:"integrations,omitempty"`
	Model        string                 `json:"model"`
	Endpoint     string                 `json:"endpoint"`
	Backend      string                 `json:"backend"`
	ChatBackend  string                 `json:"chat_backend,omitempty"`
	Context      int                    `json:"context"`
	SystemPrompt string                 `json:"system_prompt"`
	Reasoning    *bool                  `json:"reasoning,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	MCPTools     map[string]interface{} `json:"mcp_tools,omitempty"`
	DynamicTools map[string]interface{} `json:"dynamic_tools,omitempty"`
}

// BotModel represents a bot model configuration
type BotModel struct {
	Title         string           `json:"title"`
	Description   string           `json:"description"`
	Configuration BotProfileConfig `json:"configuration"`
}

// GetBotModels returns model configurations assigned to the given bot username.
func GetBotModels(DB *gorm.DB, botUsername string) ([]BotModel, error) {
	configs, err := database.GetModelConfigsForBot(DB, botUsername)
	if err != nil {
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

	models, err := GetBotModels(DB, botUser.Name)
	if err != nil {
		return err
	}

	var runtime database.BotRuntimeConfig
	runtimeErr := DB.Where("bot_user_id = ? AND is_active = ?", botUser.ID, true).Order("id desc").First(&runtime).Error

	if len(models) == 0 && runtimeErr == nil {
		var fallbackConfig BotProfileConfig
		if err := json.Unmarshal(runtime.DefaultSharedConfig, &fallbackConfig); err == nil {
			if fallbackConfig.Model != "" && fallbackConfig.Backend != "" {
				models = append(models, BotModel{
					Title:         fallbackConfig.Model,
					Description:   runtime.Description,
					Configuration: fallbackConfig,
				})
			}
		}
	}

	// Bots that route chats through an external chat backend (eg the opencode
	// integration bot) expose that chat backend on every profile model via the
	// dedicated chat_backend key. The selected model only picks which LLM the
	// external backend runs on (its provider stays in backend), so the chat
	// start page must keep resolving the backend's chat UI (eg the opencode
	// project selector) and new chats must stay bound to the chat backend
	// regardless of which model config is selected.
	if runtimeErr == nil {
		if chatBackendName := runtimeChatBackendName(runtime.DefaultSharedConfig); chatBackendName != "" {
			for i := range models {
				models[i].Configuration.ChatBackend = chatBackendName
			}
		}
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

// runtimeChatBackendName resolves the external chat backend declared by a bot
// runtime's default shared config. An explicit "chat_backend" key wins; legacy
// runtimes stored the chat backend name in "backend", which only counts when
// it names a registered external chat backend (otherwise "backend" holds the
// LLM provider, eg "deepinfra").
func runtimeChatBackendName(defaultSharedConfig []byte) string {
	config := map[string]interface{}{}
	if err := json.Unmarshal(defaultSharedConfig, &config); err != nil {
		return ""
	}
	if v, ok := config[chatstate.ChatBackendKey].(string); ok {
		if name := strings.ToLower(strings.TrimSpace(v)); name != "" {
			return name
		}
	}
	if legacyName := chatstate.ChatBackendNameFromConfig(config); legacyName != "" && IsRegisteredChatBackend(legacyName) {
		return legacyName
	}
	return ""
}

// SyncAutomatedBotProfiles refreshes public profiles for all automated bot users.
func SyncAutomatedBotProfiles(DB *gorm.DB) error {
	var bots []database.User
	if err := DB.Where("is_automated = ?", true).Find(&bots).Error; err != nil {
		return err
	}

	for _, bot := range bots {
		if err := CreateOrUpdateBotProfile(DB, bot); err != nil {
			return fmt.Errorf("failed to sync profile for bot %q: %w", bot.Name, err)
		}
	}

	return nil
}
