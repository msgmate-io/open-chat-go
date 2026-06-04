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
	deepinfraTools := []string{"get_current_time", "get_weather", "get_random_number"}
	return []BotModel{
		{
			Title:       "moonshotai/Kimi-K2.6",
			Description: "Kimi K2.6 is an open-source, native multimodal agentic model that advances practical capabilities in long-horizon coding, coding-driven design, proactive autonomous execution, and swarm-based task orchestration.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Tools:        deepinfraTools,
				Model:        "moonshotai/Kimi-K2.6",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "Qwen/Qwen3.6-27B",
			Description: "Following the February release of the Qwen3.5 series, we're pleased to share the first open-weight variant of Qwen3.6. Built on direct feedback from the community, Qwen3.6 prioritizes stability and real-world utility, offering developers a more intuitive, responsive, and genuinely productive coding experience.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Reasoning:    boolPtr(true),
				Tools:        deepinfraTools,
				Model:        "Qwen/Qwen3.6-27B",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "Qwen/Qwen3.6-35B-A3B",
			Description: "Qwen3.6-35B-A3B is Alibaba's latest flagship Mixture-of-Experts model, with 35B total parameters and only 3B activated per token (256 experts, 8 routed + 1 shared). Built on direct feedback from the community, Qwen3.6 prioritizes stability and real-world utility, offering developers a more intuitive, responsive, and genuinely productive coding experience.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Tools:        deepinfraTools,
				Model:        "Qwen/Qwen3.6-35B-A3B",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "nvidia/Nemotron-3-Nano-Omni-30B-A3B-Reasoning",
			Description: "Nemotron 3 Nano Omni is an open multimodal model built on a hybrid Mixture-of-Experts (MoE) architecture, engineered for high efficiency and strong accuracy across image, video, audio, and text inputs. It powers always-on sub-agents for computer use, document intelligence, and audio-video understanding—replacing fragmented vision, speech, and language pipelines with a single unified inference pass.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Reasoning:    boolPtr(true),
				Tools:        deepinfraTools,
				Model:        "nvidia/Nemotron-3-Nano-Omni-30B-A3B-Reasoning",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "google/gemma-4-26B-A4B-it",
			Description: "Efficient, MoE variant of Gemma 4. Gemma is a family of open models built by Google DeepMind. Gemma 4 models are multimodal, handling text and image input and generating text output.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Tools:        deepinfraTools,
				Model:        "google/gemma-4-26B-A4B-it",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "deepseek-ai/DeepSeek-V4-Flash",
			Description: "DeepSeek V4 Flash is an efficiency-focused MoE model with 284B total parameters (13B active) and a 1M-token context window. It's tuned for fast inference and high-throughput use cases while still holding up on reasoning and coding tasks.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Reasoning:    boolPtr(true),
				Tools:        deepinfraTools,
				Model:        "deepseek-ai/DeepSeek-V4-Flash",
				Endpoint:     "https://api.deepinfra.com/v1/openai",
				Backend:      "deepinfra",
				Context:      10,
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		{
			Title:       "meta-llama/Llama-3.2-11B-Vision-Instruct",
			Description: "Llama 3.2 11B Vision is a multimodal model with 11 billion parameters, designed to handle tasks combining visual and textual data. It excels in tasks such as image captioning and visual question answering, bridging the gap between language generation and visual reasoning. Pre-trained on a massive dataset of image-text pairs, it performs well in complex, high-accuracy image analysis. Its ability to integrate visual understanding with language processing makes it an ideal solution for industries requiring comprehensive visual-linguistic AI applications, such as content creation, AI-driven customer service, and research.",
			Configuration: BotProfileConfig{
				Temperature:  0.7,
				MaxTokens:    4096,
				Model:        "meta-llama/Llama-3.2-11B-Vision-Instruct",
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
