package cmd

import (
	"backend/database"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"gorm.io/gorm"
)

type ownerBotConfig struct {
	Username string `json:"username"`
}

type botIdentityConfig struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsPublic    *bool  `json:"is_public,omitempty"`
	IsActive    *bool  `json:"is_active,omitempty"`
}

type botBootstrapConfig struct {
	Owner               ownerBotConfig         `json:"owner"`
	Bot                 botIdentityConfig      `json:"bot"`
	DefaultSharedConfig map[string]interface{} `json:"default_shared_config"`
}

func loadBotBootstrapConfig(path string) (botBootstrapConfig, error) {
	var cfg botBootstrapConfig
	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed reading bot config file %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid bot config JSON in %q: %w", path, err)
	}

	if strings.TrimSpace(cfg.Owner.Username) == "" {
		return cfg, fmt.Errorf("bot config %q: owner.username is required", path)
	}
	if strings.TrimSpace(cfg.Bot.Username) == "" {
		return cfg, fmt.Errorf("bot config %q: bot.username is required", path)
	}
	if strings.TrimSpace(cfg.Bot.Name) == "" {
		return cfg, fmt.Errorf("bot config %q: bot.name is required", path)
	}
	if cfg.DefaultSharedConfig == nil {
		return cfg, fmt.Errorf("bot config %q: default_shared_config is required", path)
	}

	return cfg, nil
}

func findUserByUsername(DB *gorm.DB, username string) (*database.User, error) {
	normalized := strings.TrimSpace(username)
	if normalized == "" {
		return nil, fmt.Errorf("username is required")
	}

	var user database.User
	if err := DB.Where("email = ? OR name = ?", normalized, normalized).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user %q not found", normalized)
		}
		return nil, err
	}
	return &user, nil
}

func resolveBotUserForConfig(DB *gorm.DB, sourcePath string, cfg botBootstrapConfig, validateStrength bool) (*database.User, error) {
	username := strings.TrimSpace(cfg.Bot.Username)
	password := strings.TrimSpace(cfg.Bot.Password)

	if password == "" {
		user, err := findUserByUsername(DB, username)
		if err != nil {
			return nil, fmt.Errorf("bot config %q: bot.password omitted, so bot user must already exist: %w", sourcePath, err)
		}
		if !user.IsAutomated {
			user.IsAutomated = true
			if saveErr := DB.Save(user).Error; saveErr != nil {
				return nil, saveErr
			}
		}
		return user, nil
	}

	botCredentials := fmt.Sprintf("%s:%s", username, password)
	botUser, err := ensureBootstrapUser(DB, bootstrapUserSpec{
		Label:            fmt.Sprintf("add-bot-from-config[%s]", sourcePath),
		Credentials:      botCredentials,
		IsAdmin:          false,
		IsAutomated:      true,
		ValidateStrength: validateStrength,
	})
	if err != nil {
		return nil, err
	}
	return botUser, nil
}

func ensureOwnerConnectedToBot(DB *gorm.DB, owner database.User, bot database.User) error {
	contact := database.Contact{
		OwningUserId:  owner.ID,
		ContactUserId: bot.ID,
		ContactToken:  bot.ContactToken,
	}
	if err := DB.Where("owning_user_id = ? AND contact_user_id = ?", owner.ID, bot.ID).FirstOrCreate(&contact).Error; err != nil {
		return err
	}

	var chat database.Chat
	err := DB.Where(
		"(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
		owner.ID,
		bot.ID,
		bot.ID,
		owner.ID,
	).First(&chat).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if owner.ID < bot.ID {
		chat = database.Chat{User1Id: owner.ID, User2Id: bot.ID}
	} else {
		chat = database.Chat{User1Id: bot.ID, User2Id: owner.ID}
	}
	return DB.Create(&chat).Error
}

func applyBotBootstrapConfig(DB *gorm.DB, sourcePath string, cfg botBootstrapConfig, validateStrength bool) error {
	owner, err := findUserByUsername(DB, cfg.Owner.Username)
	if err != nil {
		return fmt.Errorf("bot config %q: owner user must already exist: %w", sourcePath, err)
	}

	botUser, err := resolveBotUserForConfig(DB, sourcePath, cfg, validateStrength)
	if err != nil {
		return err
	}

	configData, err := json.Marshal(cfg.DefaultSharedConfig)
	if err != nil {
		return fmt.Errorf("bot config %q: failed to marshal default_shared_config: %w", sourcePath, err)
	}

	isPublic := false
	if cfg.Bot.IsPublic != nil {
		isPublic = *cfg.Bot.IsPublic
	}
	isActive := true
	if cfg.Bot.IsActive != nil {
		isActive = *cfg.Bot.IsActive
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var runtime database.BotRuntimeConfig
		err := tx.Where("owner_user_id = ? AND name = ?", owner.ID, cfg.Bot.Name).First(&runtime).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			runtime = database.BotRuntimeConfig{
				BotUserId:           botUser.ID,
				OwnerUserId:         owner.ID,
				Name:                cfg.Bot.Name,
				Description:         cfg.Bot.Description,
				DefaultSharedConfig: configData,
				IsPublic:            isPublic,
				IsActive:            isActive,
			}
			if err := tx.Create(&runtime).Error; err != nil {
				return err
			}
		} else {
			updates := map[string]interface{}{
				"bot_user_id":           botUser.ID,
				"description":           cfg.Bot.Description,
				"default_shared_config": configData,
				"is_public":             isPublic,
				"is_active":             isActive,
			}
			if err := tx.Model(&runtime).Updates(updates).Error; err != nil {
				return err
			}
		}

		if err := ensureOwnerConnectedToBot(tx, *owner, *botUser); err != nil {
			return err
		}

		return nil
	})
}

func applyBotBootstrapConfigFiles(DB *gorm.DB, paths []string, validateStrength bool) error {
	for i, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		cfg, err := loadBotBootstrapConfig(trimmed)
		if err != nil {
			return fmt.Errorf("add-bot-from-config[%d]: %w", i, err)
		}
		if err := applyBotBootstrapConfig(DB, trimmed, cfg, validateStrength); err != nil {
			return fmt.Errorf("add-bot-from-config[%d]: %w", i, err)
		}
	}
	return nil
}
