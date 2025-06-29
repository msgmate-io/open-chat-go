package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gorm.io/gorm"
)

// userBootstrapConfig declares a single user to ensure at server startup via
// the open-chat bootstrap config ("bootstrap.users"). It mirrors the simple
// username:password bootstrap model used by CREATE_EXTRA_USER /
// CREATE_EXTRA_BOT, but is declared declaratively in open-chat.json.
type userBootstrapConfig struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Email       string `json:"email,omitempty"`
	IsAdmin     bool   `json:"is_admin,omitempty"`
	IsAutomated bool   `json:"is_automated,omitempty"`
}

// loadUserBootstrapConfigsFromSpec parses a user bootstrap spec. The spec may
// be an inline JSON object, an inline JSON array of objects, or a filesystem
// path to a JSON file containing either shape.
func loadUserBootstrapConfigsFromSpec(spec string) ([]userBootstrapConfig, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return []userBootstrapConfig{}, nil
	}

	content := []byte(trimmed)
	source := "inline"
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		raw, err := os.ReadFile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("failed reading user config file %q: %w", trimmed, err)
		}
		content = raw
		source = trimmed
	}

	content = bytes.TrimSpace(content)
	if len(content) == 0 {
		return []userBootstrapConfig{}, nil
	}

	configs := []userBootstrapConfig{}
	if content[0] == '[' {
		if err := json.Unmarshal(content, &configs); err != nil {
			return nil, fmt.Errorf("invalid user config array JSON in %q: %w", source, err)
		}
	} else {
		single := userBootstrapConfig{}
		if err := json.Unmarshal(content, &single); err != nil {
			return nil, fmt.Errorf("invalid user config JSON in %q: %w", source, err)
		}
		configs = append(configs, single)
	}

	return configs, nil
}

// applyUserBootstrapConfigFiles ensures every user declared in the given specs.
// Each spec may declare one or many users. Users are created through the same
// ensureBootstrapUser path as the root/bot/extra credentials, so "random" and
// "hashed_" password forms behave identically.
func applyUserBootstrapConfigFiles(DB *gorm.DB, specs []string, validateStrength bool) error {
	for i, spec := range specs {
		trimmed := strings.TrimSpace(spec)
		if trimmed == "" {
			continue
		}

		configs, err := loadUserBootstrapConfigsFromSpec(trimmed)
		if err != nil {
			return fmt.Errorf("bootstrap.users[%d]: %w", i, err)
		}

		for j, cfg := range configs {
			username := strings.TrimSpace(cfg.Username)
			if username == "" {
				return fmt.Errorf("bootstrap.users[%d][%d]: username is required", i, j)
			}
			if strings.TrimSpace(cfg.Password) == "" {
				return fmt.Errorf("bootstrap.users[%d][%d] (%s): password is required", i, j, username)
			}

			label := fmt.Sprintf("bootstrap.users[%d][%d] (%s)", i, j, username)
			if _, err := ensureBootstrapUser(DB, bootstrapUserSpec{
				Label:            label,
				Credentials:      username + ":" + cfg.Password,
				Email:            cfg.Email,
				IsAdmin:          cfg.IsAdmin,
				IsAutomated:      cfg.IsAutomated,
				ValidateStrength: validateStrength,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
