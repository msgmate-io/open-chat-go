package cmd

import (
	"backend/database"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strings"

	"gorm.io/gorm"
)

type botIdentityConfig struct {
	Username    string `json:"username"`
	Email       string `json:"email,omitempty"`
	Password    string `json:"password"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsPublic    *bool  `json:"is_public,omitempty"`
	IsActive    *bool  `json:"is_active,omitempty"`
}

type botBootstrapConfig struct {
	PrimaryOwner        string                 `json:"primary_owner"`
	AdditionalOwners    []string               `json:"additional_owners,omitempty"`
	Bot                 botIdentityConfig      `json:"bot"`
	DefaultSharedConfig map[string]interface{} `json:"default_shared_config"`
	OverwriteIfExists   bool                   `json:"overwrite_if_exists,omitempty"`
}

func normalizeOwnerUsernames(cfg botBootstrapConfig) []string {
	ordered := []string{}
	seen := map[string]struct{}{}
	appendOwner := func(username string) {
		trimmed := strings.TrimSpace(username)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		ordered = append(ordered, trimmed)
	}

	appendOwner(cfg.PrimaryOwner)
	for _, username := range cfg.AdditionalOwners {
		appendOwner(username)
	}

	return ordered
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

	ownerUsernames := normalizeOwnerUsernames(cfg)
	if strings.TrimSpace(cfg.PrimaryOwner) == "" {
		return cfg, fmt.Errorf("bot config %q: primary_owner is required", path)
	}
	if len(ownerUsernames) == 0 {
		return cfg, fmt.Errorf("bot config %q: primary_owner/additional_owners must include at least one owner", path)
	}
	if strings.TrimSpace(cfg.Bot.Username) == "" {
		return cfg, fmt.Errorf("bot config %q: bot.username is required", path)
	}
	if strings.TrimSpace(cfg.Bot.Name) == "" {
		return cfg, fmt.Errorf("bot config %q: bot.name is required", path)
	}
	if email := strings.TrimSpace(strings.ToLower(cfg.Bot.Email)); email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return cfg, fmt.Errorf("bot config %q: bot.email must be a valid email", path)
		}
		cfg.Bot.Email = email
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
	if err := DB.Where("username = ?", normalized).First(&user).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err := DB.Where("email = ? OR name = ?", normalized, normalized).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("user %q not found", normalized)
			}
			return nil, err
		}
	}
	if strings.TrimSpace(user.Username) == "" {
		if err := DB.Model(&user).Update("username", normalized).Error; err != nil {
			return nil, err
		}
		user.Username = normalized
	}
	return &user, nil
}

func resolveBotUserForConfig(DB *gorm.DB, sourcePath string, cfg botBootstrapConfig, validateStrength bool) (*database.User, error) {
	username := strings.TrimSpace(cfg.Bot.Username)
	botEmail := strings.TrimSpace(strings.ToLower(cfg.Bot.Email))
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
		if botEmail != "" && !strings.EqualFold(strings.TrimSpace(user.Email), botEmail) {
			var existing database.User
			if err := DB.Where("email = ? AND id <> ?", botEmail, user.ID).First(&existing).Error; err == nil {
				return nil, fmt.Errorf("bot config %q: bot.email already in use", sourcePath)
			} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			if saveErr := DB.Model(user).Update("email", botEmail).Error; saveErr != nil {
				return nil, saveErr
			}
			user.Email = botEmail
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
	if botEmail != "" && !strings.EqualFold(strings.TrimSpace(botUser.Email), botEmail) {
		var existing database.User
		if err := DB.Where("email = ? AND id <> ?", botEmail, botUser.ID).First(&existing).Error; err == nil {
			return nil, fmt.Errorf("bot config %q: bot.email already in use", sourcePath)
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if saveErr := DB.Model(botUser).Update("email", botEmail).Error; saveErr != nil {
			return nil, saveErr
		}
		botUser.Email = botEmail
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
	return nil
}

func applyBotBootstrapConfig(DB *gorm.DB, sourcePath string, cfg botBootstrapConfig, validateStrength bool) error {
	ownerUsernames := normalizeOwnerUsernames(cfg)
	ownersByUsername := map[string]*database.User{}
	for _, ownerUsername := range ownerUsernames {
		owner, err := findUserByUsername(DB, ownerUsername)
		if err != nil {
			return fmt.Errorf("bot config %q: owner user must already exist: %w", sourcePath, err)
		}
		ownersByUsername[ownerUsername] = owner
	}

	primaryOwnerUsername := strings.TrimSpace(cfg.PrimaryOwner)
	owner := ownersByUsername[primaryOwnerUsername]

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
		} else if cfg.OverwriteIfExists {
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

		for _, ownerUsername := range ownerUsernames {
			configuredOwner := ownersByUsername[ownerUsername]
			if configuredOwner == nil {
				continue
			}
			if err := database.EnsureBotRuntimeOwner(tx, runtime.ID, configuredOwner.ID); err != nil {
				return err
			}
			if err := ensureOwnerConnectedToBot(tx, *configuredOwner, *botUser); err != nil {
				return err
			}
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
