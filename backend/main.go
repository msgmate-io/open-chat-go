package main

import (
	"backend/cmd"
	"backend/integrations"
	"backend/runtimecfg"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	ufcli "github.com/urfave/cli/v3"
)

type openChatConfigBootstrap struct {
	Spec        string
	OverrideEnv bool
}

type openChatConfig struct {
	Env          map[string]interface{}            `json:"env"`
	Integrations map[string]map[string]interface{} `json:"integrations"`
	Bootstrap    *openChatBootstrapConfig          `json:"bootstrap,omitempty"`
}

type openChatBootstrapConfig struct {
	Bots json.RawMessage             `json:"bots,omitempty"`
	SSH  *openChatSSHBootstrapConfig `json:"ssh,omitempty"`
}

type openChatSSHBootstrapConfig struct {
	Owner   string          `json:"owner,omitempty"`
	Owners  []string        `json:"owners,omitempty"`
	Keys    json.RawMessage `json:"keys,omitempty"`
	Servers json.RawMessage `json:"servers,omitempty"`
}

func parseOpenChatConfigBootstrap(args []string) openChatConfigBootstrap {
	cfg := openChatConfigBootstrap{}

	for i := 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--config":
			if i+1 < len(args) {
				cfg.Spec = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--config="):
			cfg.Spec = strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		case arg == "--config-override-env":
			cfg.OverrideEnv = true
		case strings.HasPrefix(arg, "--config-override-env="):
			v := strings.TrimSpace(strings.TrimPrefix(arg, "--config-override-env="))
			cfg.OverrideEnv = !(v == "" || strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "no"))
		}
	}

	if strings.TrimSpace(cfg.Spec) == "" {
		cfg.Spec = strings.TrimSpace(os.Getenv("OPEN_CHAT_CONFIG"))
	}
	if !cfg.OverrideEnv {
		overrideEnv := strings.TrimSpace(os.Getenv("OPEN_CHAT_CONFIG_OVERRIDE_ENV"))
		if strings.EqualFold(overrideEnv, "true") || overrideEnv == "1" || strings.EqualFold(overrideEnv, "yes") {
			cfg.OverrideEnv = true
		}
	}

	if strings.TrimSpace(cfg.Spec) == "" {
		if file := firstExistingPath([]string{".open-chat.json", "open-chat.json"}); file != "" {
			cfg.Spec = file
		}
	}

	return cfg
}

func firstExistingPath(paths []string) string {
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if info, err := os.Stat(trimmed); err == nil && !info.IsDir() {
			return trimmed
		}
	}
	return ""
}

func resolveConfigSource(raw string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), "inline --config JSON", nil
	}
	content, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, "", fmt.Errorf("failed reading config path %q: %w", trimmed, err)
	}
	return content, trimmed, nil
}

func loadOpenChatConfig(raw []byte, source string) (openChatConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return openChatConfig{}, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	cfg := openChatConfig{}
	if err := decoder.Decode(&cfg); err != nil {
		return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): %w", source, err)
	}

	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): unexpected trailing JSON", source)
		}
		return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): %w", source, err)
	}

	return cfg, nil
}

func normalizeTokenForEnv(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	b := strings.Builder{}
	lastUnderscore := false
	for _, r := range upper {
		isAlpha := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func deriveIntegrationEnvKey(integrationName, key string) string {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(key)), "OCI_") {
		return strings.ToUpper(strings.TrimSpace(key))
	}
	integrationPart := normalizeTokenForEnv(integrationName)
	keyPart := normalizeTokenForEnv(key)
	if integrationPart == "" || keyPart == "" {
		return ""
	}
	return "OCI_" + integrationPart + "_" + keyPart
}

func flattenConfigEnv(cfg openChatConfig) (map[string]string, error) {
	integrations.EnsureLoaded()

	updates := map[string]string{}
	for key, value := range cfg.Env {
		normalized := strings.TrimSpace(key)
		if normalized == "" {
			continue
		}
		updates[normalized] = fmt.Sprintf("%v", value)
	}

	runtimeEnvByIntegration := map[string]map[string]struct{}{}
	for _, decl := range integrations.RuntimeEnvDeclarations() {
		integrationName := strings.ToLower(strings.TrimSpace(decl.IntegrationName))
		if _, ok := runtimeEnvByIntegration[integrationName]; !ok {
			runtimeEnvByIntegration[integrationName] = map[string]struct{}{}
		}
		runtimeEnvByIntegration[integrationName][strings.ToUpper(strings.TrimSpace(decl.Key))] = struct{}{}
	}

	aliasByIntegration := map[string]map[string]string{}
	for _, decl := range integrations.RuntimeConfigAliasDeclarations() {
		integrationName := strings.ToLower(strings.TrimSpace(decl.IntegrationName))
		if _, ok := aliasByIntegration[integrationName]; !ok {
			aliasByIntegration[integrationName] = map[string]string{}
		}
		aliasByIntegration[integrationName][strings.ToLower(strings.TrimSpace(decl.JSONKey))] = strings.ToUpper(strings.TrimSpace(decl.EnvKey))
	}

	for integrationNameRaw, values := range cfg.Integrations {
		integrationName := strings.ToLower(strings.TrimSpace(integrationNameRaw))
		if integrationName == "" {
			continue
		}
		declaredEnv, ok := runtimeEnvByIntegration[integrationName]
		if !ok {
			return nil, fmt.Errorf("config integrations.%s: integration has no declared runtime env vars or is unknown", integrationNameRaw)
		}

		aliases := aliasByIntegration[integrationName]
		for key, value := range values {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}

			directEnvKey := strings.ToUpper(trimmedKey)
			envKey := ""
			if _, exists := declaredEnv[directEnvKey]; exists {
				envKey = directEnvKey
			} else if alias, exists := aliases[strings.ToLower(trimmedKey)]; exists {
				envKey = alias
			} else {
				derived := deriveIntegrationEnvKey(integrationName, trimmedKey)
				if _, exists := declaredEnv[derived]; exists {
					envKey = derived
				}
			}

			if envKey == "" {
				return nil, fmt.Errorf("config integrations.%s.%s does not map to a declared runtime env var", integrationNameRaw, key)
			}
			updates[envKey] = fmt.Sprintf("%v", value)
		}
	}

	return updates, nil
}

func normalizeOwners(raw []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	for _, owner := range raw {
		trimmed := strings.TrimSpace(owner)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func toOpenChatBootstrapRuntime(cfg openChatConfig) runtimecfg.OpenChatBootstrap {
	out := runtimecfg.OpenChatBootstrap{}
	if cfg.Bootstrap == nil {
		return out
	}

	if len(bytes.TrimSpace(cfg.Bootstrap.Bots)) > 0 {
		out.BotSpecs = append(out.BotSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Bots)))
	}

	if cfg.Bootstrap.SSH != nil {
		owners := append([]string{}, cfg.Bootstrap.SSH.Owners...)
		if strings.TrimSpace(cfg.Bootstrap.SSH.Owner) != "" {
			owners = append(owners, cfg.Bootstrap.SSH.Owner)
		}
		out.SSHDefaultOwners = normalizeOwners(owners)

		if len(bytes.TrimSpace(cfg.Bootstrap.SSH.Keys)) > 0 {
			out.SSHKeySpecs = append(out.SSHKeySpecs, string(bytes.TrimSpace(cfg.Bootstrap.SSH.Keys)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.SSH.Servers)) > 0 {
			out.SSHServerSpecs = append(out.SSHServerSpecs, string(bytes.TrimSpace(cfg.Bootstrap.SSH.Servers)))
		}
	}

	return out
}

func applyEnvUpdates(updates map[string]string, overrideEnv bool) error {
	for key, value := range updates {
		normalized := strings.TrimSpace(key)
		if normalized == "" {
			continue
		}
		if !overrideEnv {
			if _, exists := os.LookupEnv(normalized); exists {
				continue
			}
		}
		if err := os.Setenv(normalized, value); err != nil {
			return fmt.Errorf("failed setting env %q from config: %w", normalized, err)
		}
	}
	return nil
}

func applyOpenChatConfigBootstrap(args []string) error {
	bootstrap := parseOpenChatConfigBootstrap(args)
	raw, source, err := resolveConfigSource(bootstrap.Spec)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	cfg, err := loadOpenChatConfig(raw, source)
	if err != nil {
		return err
	}
	updates, err := flattenConfigEnv(cfg)
	if err != nil {
		return err
	}
	if err := applyEnvUpdates(updates, bootstrap.OverrideEnv); err != nil {
		return err
	}
	runtimecfg.SetOpenChatBootstrap(toOpenChatBootstrapRuntime(cfg))

	return nil
}

//	@title							Open Chat API
//	@version						1.0
//	@description					API for Open Chat application
//
//	@tag.name						chats
//	@tag.description				Chats hold a collection of messages and files or meta-data, they are central to how open-chat works and are used to hold information for interactions and integratins
//
// 	@tag.name						messages
//	@tag.description				Messages are the atomic data point of open-chat, they may hold any sort of supported information, they may also reference information in external locations. Messages are collected in a chat. Messages can have only one creator/sender but are received by all chat members.
//
//	@tag.name						users
//	@tag.description				Everything user management related, users are also used to abstract access permissions. Chats have users as participants, only users share each others contact may create a shared chat.
//
//	@tag.name					bots
//	@tag.description				Owner-scoped automated bot management and interaction creation.
//
//	@securityDefinitions.apikey	SessionAuth
//	@in								cookie
//	@name							session_id
//	@description					Session cookie obtained from login endpoint

func main() {
	if len(os.Args) == 1 {
		os.Args = append(os.Args, "--help")
	}

	if err := applyOpenChatConfigBootstrap(os.Args); err != nil {
		log.Fatal(err)
	}

	rootCmd := &ufcli.Command{
		Name:  "open-chat",
		Usage: "Open Chat command line interface",
		Flags: []ufcli.Flag{
			&ufcli.StringFlag{
				Name:    "config",
				Usage:   "Inline open-chat JSON config object or filesystem path to open-chat config JSON",
				Sources: ufcli.EnvVars("OPEN_CHAT_CONFIG"),
			},
			&ufcli.BoolFlag{
				Name:    "config-override-env",
				Usage:   "Allow open-chat config values to overwrite already-set environment variables",
				Value:   false,
				Sources: ufcli.EnvVars("OPEN_CHAT_CONFIG_OVERRIDE_ENV"),
			},
		},
		Commands: []*ufcli.Command{
			cmd.ServerCli(),
			cmd.WorkerCli(),
			cmd.ClientCli(),
		},
	}

	if err := rootCmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
