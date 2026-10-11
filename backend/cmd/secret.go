package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"backend/secretstore"

	"github.com/urfave/cli/v3"
)

// SecretCli returns the `open-chat secret` command group used to seal values
// and manage the config document's `secrets:` section.
func SecretCli() *cli.Command {
	return &cli.Command{
		Name:  "secret",
		Usage: "seal sensitive values and manage encrypted config secrets",
		Commands: []*cli.Command{
			secretSealCli(),
			secretMigrateConfigCli(),
			secretRekeyCli(),
		},
	}
}

func secretPasswordFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "password", Usage: "secret password (prefer OPEN_CHAT_SECRET_PASSWORD or --password-file)", Sources: cli.EnvVars("OPEN_CHAT_SECRET_PASSWORD")},
		&cli.StringFlag{Name: "password-file", Usage: "path to a file containing the secret password", Sources: cli.EnvVars("OPEN_CHAT_SECRET_PASSWORD_FILE")},
	}
}

func resolveCLISecretPassword(c *cli.Command) (string, error) {
	if password := strings.TrimSpace(c.String("password")); password != "" {
		return password, nil
	}
	if file := strings.TrimSpace(c.String("password-file")); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("failed reading password file %q: %w", file, err)
		}
		return strings.TrimRight(string(raw), "\r\n"), nil
	}
	return "", nil
}

func authorizeActiveProvider(ctx context.Context, password string) (secretstore.Provider, error) {
	provider := secretstore.Active()
	if provider == nil {
		return nil, secretstore.ErrNoProvider
	}
	if _, err := provider.Authorize(ctx, secretstore.AuthParams{SessionKey: secretstore.BootSessionKey, Password: password}); err != nil {
		return nil, err
	}
	return provider, nil
}

func secretSealCli() *cli.Command {
	flags := append(secretPasswordFlags(),
		&cli.StringFlag{Name: "scope", Usage: "block scope (default: config)", Value: "config"},
		&cli.StringFlag{Name: "label", Usage: "non-secret label for the block"},
		&cli.StringFlag{Name: "algorithm", Usage: "override the block algorithm"},
	)
	return &cli.Command{
		Name:  "seal",
		Usage: "read a plaintext value from stdin and print the sealed block JSON",
		Flags: flags,
		Action: func(ctx context.Context, c *cli.Command) error {
			plaintext, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			plaintext = bytes.TrimRight(plaintext, "\r\n")
			if len(plaintext) == 0 {
				return errors.New("no plaintext provided on stdin")
			}
			password, err := resolveCLISecretPassword(c)
			if err != nil {
				return err
			}
			provider, err := authorizeActiveProvider(ctx, password)
			if err != nil {
				return err
			}
			block, err := provider.Seal(ctx, secretstore.SealParams{
				Scope:     c.String("scope"),
				Label:     c.String("label"),
				Algorithm: c.String("algorithm"),
				Plaintext: plaintext,
			})
			if err != nil {
				return err
			}
			// Never print the plaintext; only the non-secret ref and block.
			out := map[string]interface{}{
				"ref":       block.Ref,
				"algorithm": block.Algorithm,
				"scope":     block.Scope,
				"label":     block.Label,
				"secrets": map[string]interface{}{
					block.Ref: block,
				},
			}
			encoded, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(c.Writer, string(encoded))
			return nil
		},
	}
}

func secretRekeyCli() *cli.Command {
	flags := append(secretPasswordFlags(),
		&cli.StringFlag{Name: "config", Usage: "path to the open-chat config JSON document", Required: true},
		&cli.StringFlag{Name: "new-password", Usage: "new secret password", Sources: cli.EnvVars("OPEN_CHAT_SECRET_NEW_PASSWORD")},
		&cli.StringFlag{Name: "new-password-file", Usage: "path to a file containing the new secret password", Sources: cli.EnvVars("OPEN_CHAT_SECRET_NEW_PASSWORD_FILE")},
	)
	return &cli.Command{
		Name:  "rekey",
		Usage: "re-encrypt every config secret block under a new password (JSON config only)",
		Flags: flags,
		Action: func(ctx context.Context, c *cli.Command) error {
			oldPassword, err := resolveCLISecretPassword(c)
			if err != nil {
				return err
			}
			newPassword := strings.TrimSpace(c.String("new-password"))
			if newPassword == "" {
				if file := strings.TrimSpace(c.String("new-password-file")); file != "" {
					raw, err := os.ReadFile(file)
					if err != nil {
						return fmt.Errorf("failed reading new password file %q: %w", file, err)
					}
					newPassword = strings.TrimRight(string(raw), "\r\n")
				}
			}
			if strings.TrimSpace(newPassword) == "" {
				return errors.New("a new password is required (--new-password or --new-password-file)")
			}
			doc, err := readConfigJSON(c.String("config"))
			if err != nil {
				return err
			}
			blocks, err := extractSecretBlocks(doc)
			if err != nil {
				return err
			}
			if len(blocks) == 0 {
				return errors.New("config has no `secrets` section")
			}
			oldProvider, err := authorizeActiveProvider(ctx, oldPassword)
			if err != nil {
				return err
			}
			newProvider, err := authorizeActiveProvider(ctx, newPassword)
			if err != nil {
				return err
			}
			rekeyed := map[string]secretstore.Block{}
			for ref, block := range blocks {
				if strings.TrimSpace(block.Ref) == "" {
					block.Ref = ref
				}
				plaintext, err := oldProvider.Unseal(ctx, block)
				if err != nil {
					return fmt.Errorf("failed to unseal block %q: %w", ref, err)
				}
				resealed, err := newProvider.Seal(ctx, secretstore.SealParams{
					OwnerUUID: block.OwnerUUID,
					Scope:     block.Scope,
					Label:     block.Label,
					Algorithm: block.Algorithm,
					Plaintext: plaintext,
				})
				if err != nil {
					return fmt.Errorf("failed to reseal block %q: %w", ref, err)
				}
				rekeyed[ref] = resealed
			}
			doc["secrets"] = rekeyed
			if err := writeConfigJSONAtomic(c.String("config"), doc); err != nil {
				return err
			}
			fmt.Fprintf(c.Writer, "rekeyed %d secret block(s)\n", len(rekeyed))
			return nil
		},
	}
}

func secretMigrateConfigCli() *cli.Command {
	flags := append(secretPasswordFlags(),
		&cli.StringFlag{Name: "config", Usage: "path to the open-chat config JSON document", Required: true},
		&cli.StringSliceFlag{Name: "key", Usage: "config path to seal, e.g. env.OPENAI_API_KEY or integrations.git.token; repeatable"},
		&cli.StringFlag{Name: "scope", Usage: "block scope (default: config)", Value: "config"},
	)
	return &cli.Command{
		Name:  "migrate-config",
		Usage: "replace plaintext config values with sealed $tvault refs (JSON config only)",
		Flags: flags,
		Action: func(ctx context.Context, c *cli.Command) error {
			keys := c.StringSlice("key")
			if len(keys) == 0 {
				return errors.New("at least one --key is required")
			}
			password, err := resolveCLISecretPassword(c)
			if err != nil {
				return err
			}
			provider, err := authorizeActiveProvider(ctx, password)
			if err != nil {
				return err
			}
			doc, err := readConfigJSON(c.String("config"))
			if err != nil {
				return err
			}
			secrets, err := extractSecretBlocks(doc)
			if err != nil {
				return err
			}
			for _, keyPath := range keys {
				value, found, err := lookupConfigPath(doc, keyPath)
				if err != nil {
					return err
				}
				if !found {
					return fmt.Errorf("config key %q not found", keyPath)
				}
				plaintext, ok := value.(string)
				if !ok {
					return fmt.Errorf("config key %q is not a string", keyPath)
				}
				block, err := provider.Seal(ctx, secretstore.SealParams{
					Scope:     c.String("scope"),
					Label:     keyPath,
					Plaintext: []byte(plaintext),
				})
				if err != nil {
					return err
				}
				secrets[block.Ref] = block
				if err := setConfigPath(doc, keyPath, map[string]interface{}{tvaultRefKeyName: block.Ref}); err != nil {
					return err
				}
			}
			doc["secrets"] = secrets
			if err := writeConfigJSONAtomic(c.String("config"), doc); err != nil {
				return err
			}
			fmt.Fprintf(c.Writer, "sealed %d config value(s)\n", len(keys))
			return nil
		},
	}
}

const tvaultRefKeyName = "$tvault"

func readConfigJSON(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc := map[string]interface{}{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("config %q is not valid JSON: %w", path, err)
	}
	return doc, nil
}

func writeConfigJSONAtomic(path string, doc map[string]interface{}) error {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func extractSecretBlocks(doc map[string]interface{}) (map[string]secretstore.Block, error) {
	out := map[string]secretstore.Block{}
	raw, ok := doc["secrets"]
	if !ok || raw == nil {
		return out, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("invalid `secrets` section: %w", err)
	}
	return out, nil
}

func lookupConfigPath(doc map[string]interface{}, keyPath string) (interface{}, bool, error) {
	parts := strings.Split(keyPath, ".")
	current := interface{}(doc)
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, fmt.Errorf("config key %q traverses a non-object", keyPath)
		}
		next, ok := m[part]
		if !ok {
			return nil, false, nil
		}
		current = next
	}
	return current, true, nil
}

func setConfigPath(doc map[string]interface{}, keyPath string, value interface{}) error {
	parts := strings.Split(keyPath, ".")
	current := doc
	for idx, part := range parts {
		if idx == len(parts)-1 {
			current[part] = value
			return nil
		}
		next, ok := current[part].(map[string]interface{})
		if !ok {
			return fmt.Errorf("config key %q traverses a non-object", keyPath)
		}
		current = next
	}
	return nil
}
