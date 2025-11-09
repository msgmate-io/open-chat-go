package msgmate

import (
	"backend/database"
	"encoding/json"
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

// GetDefaultBotModels returns the default bot model configurations
func GetDefaultBotModels() []BotModel {
	return []BotModel{
		{
			Title:       "gpt-4o",
			Description: "OpenAI's GPT-4o, optimized for specific applications with advanced tool and function support.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Tools:        []string{"get_weather", "get_current_time"},
				Model:        "gpt-4o",
				Endpoint:     "https://api.openai.com/v1/",
				Backend:      "openai",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "o3-mini-2025-01-31",
			Description: "OpenAI's O3 Mini, a powerful and efficient language model.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Model:        "o3-mini-2025-01-31",
				Endpoint:     "https://api.openai.com/v1/",
				Backend:      "openai",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "meta-llama/Llama-3.3-70B-Instruct-Turbo",
			Description: "Meta's Llama 3.3, a powerful and efficient language model.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Model:        "meta-llama/Llama-3.3-70B-Instruct-Turbo",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Tools:        []string{"get_current_time", "get_weather", "get_random_number"},
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
			Description: "Meta's Llama 3.1, a powerful and efficient language model.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Model:        "meta-llama/Llama-3.3-70B-Instruct-Turbo",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Tools:        []string{"get_current_time", "get_weather", "get_random_number"},
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "deepseek-ai/DeepSeek-V3",
			Description: "DeepSeek's DeepSeek V3, a powerful and efficient language model.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Model:        "deepseek-ai/DeepSeek-V3",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "deepseek-ai/DeepSeek-R1",
			Description: "DeepSeek's DeepSeek Coder, a powerful and efficient language model.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Reasoning:    boolPtr(true),
				Model:        "deepseek-ai/DeepSeek-R1",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "meta-llama/Meta-Llama-3.1-405B-Instruct",
			Description: "Meta's Llama 3.1, a powerful and efficient language model.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Reasoning:    boolPtr(false),
				Tools:        []string{"get_current_time", "get_weather", "get_random_number"},
				Model:        "meta-llama/Meta-Llama-3.1-405B-Instruct",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "meta-llama/Meta-Llama-3.1-8B-Instruct",
			Description: "Meta's Llama 3.1, a powerful and efficient language model.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Reasoning:    boolPtr(false),
				Tools:        []string{"get_current_time", "get_weather", "get_random_number"},
				Model:        "meta-llama/Meta-Llama-3.1-8B-Instruct",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
	}
}

// boolPtr returns a pointer to a boolean value
func boolPtr(b bool) *bool {
	return &b
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

	// Get default bot models
	models := GetDefaultBotModels()

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
