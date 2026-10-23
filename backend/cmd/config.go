package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// configSetOperation mirrors integrationsettings.ConfigSetOp for the CLI's
// request body without importing the server-side persistence package.
type configSetOperation struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// configUpsertOperation mirrors integrationsettings.ConfigUpsertOp.
type configUpsertOperation struct {
	Path  string                 `json:"path"`
	Key   string                 `json:"key"`
	Value map[string]interface{} `json:"value"`
}

type configDeltaRequest struct {
	Set    []configSetOperation    `json:"set,omitempty"`
	Upsert []configUpsertOperation `json:"upsert,omitempty"`
}

type configMutateResponse struct {
	Persisted       bool   `json:"persisted"`
	PersistError    string `json:"persist_error"`
	RestartRequired bool   `json:"restart_required"`
	RemotePersisted bool   `json:"remote_persisted"`
	RemoteTarget    string `json:"remote_target"`
	RemoteError     string `json:"remote_error"`
}

// ConfigCli groups the configuration-management subcommands.
func ConfigCli() *cli.Command {
	return &cli.Command{
		Name:     "config",
		Usage:    "manage the running server configuration",
		Commands: []*cli.Command{configSetCli()},
	}
}

func configSetCli() *cli.Command {
	return &cli.Command{
		Name:  "set",
		Usage: "set one value in the running server config (single set/upsert op) via the admin API",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "path",
				Usage:    "target path expression, e.g. anchors.little-world-odoo-admin-kubeconfig-b64 or bootstrap.bots[bot.name=odoo-plan-bot].default_shared_config.opencode_project",
				Required: true,
				Sources:  cli.EnvVars("OPEN_CHAT_CONFIG_PATH"),
			},
			&cli.StringFlag{
				Name:    "value",
				Usage:   "value to set (ignored with --value-file/--value-stdin)",
				Sources: cli.EnvVars("OPEN_CHAT_CONFIG_VALUE"),
			},
			&cli.StringFlag{
				Name:  "value-file",
				Usage: "read the value from a file (raw string unless --json)",
			},
			&cli.BoolFlag{
				Name:  "value-stdin",
				Usage: "read the value from stdin",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "parse the value as JSON (required for --upsert and for non-string scalars/objects)",
			},
			&cli.BoolFlag{
				Name:  "upsert",
				Usage: "upsert a mapping into the list at --path, matched by --key (value must be a JSON object containing the key)",
			},
			&cli.StringFlag{
				Name:  "key",
				Usage: "list item key field used by --upsert",
			},
			&cli.StringFlag{
				Name:    "base-url",
				Usage:   "full server base URL, e.g. https://ci.msgmate.io (overrides --host/--port)",
				Sources: cli.EnvVars("OPEN_CHAT_BASE_URL"),
			},
			&cli.StringFlag{
				Name:    "host",
				Usage:   "backend host (http) when --base-url is not set",
				Value:   "127.0.0.1",
				Sources: cli.EnvVars("HOST"),
			},
			&cli.Uint16Flag{
				Name:    "port",
				Usage:   "backend port when --base-url is not set",
				Value:   1984,
				Sources: cli.EnvVars("PORT"),
			},
			&cli.StringFlag{
				Name:    "api-token",
				Usage:   "admin access token used as Authorization: Bearer",
				Sources: cli.EnvVars("OPEN_CHAT_API_TOKEN"),
			},
			&cli.StringFlag{
				Name:    "username",
				Usage:   "login username (used only when no --api-token is provided)",
				Sources: cli.EnvVars("OPEN_CHAT_USERNAME"),
			},
			&cli.StringFlag{
				Name:    "password",
				Usage:   "login password (used only when no --api-token is provided)",
				Sources: cli.EnvVars("OPEN_CHAT_PASSWORD"),
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			path := strings.TrimSpace(c.String("path"))
			if path == "" {
				return fmt.Errorf("--path is required")
			}

			rawValue, err := resolveConfigSetValue(c)
			if err != nil {
				return err
			}

			baseURL := strings.TrimSpace(c.String("base-url"))
			if baseURL == "" {
				baseURL = fmt.Sprintf("http://%s:%d", strings.TrimSpace(c.String("host")), c.Uint16("port"))
			}

			api, err := newRunHTTPClient(baseURL, c.String("api-token"), 30*time.Second)
			if err != nil {
				return err
			}
			if err := loginIfNeeded(ctx, api, strings.TrimSpace(c.String("username")), c.String("password")); err != nil {
				return err
			}

			request := configDeltaRequest{}
			if c.Bool("upsert") {
				key := strings.TrimSpace(c.String("key"))
				if key == "" {
					return fmt.Errorf("--key is required with --upsert")
				}
				if !c.Bool("json") {
					return fmt.Errorf("--upsert requires --json so the value is parsed as a JSON object")
				}
				value := map[string]interface{}{}
				if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
					return fmt.Errorf("--upsert value must be a JSON object: %w", err)
				}
				request.Upsert = []configUpsertOperation{{Path: path, Key: key, Value: value}}
			} else {
				var parsed interface{} = rawValue
				if c.Bool("json") {
					if err := json.Unmarshal([]byte(rawValue), &parsed); err != nil {
						return fmt.Errorf("--json value is not valid JSON: %w", err)
					}
				}
				request.Set = []configSetOperation{{Path: path, Value: parsed}}
			}

			var response configMutateResponse
			if _, err := api.requestJSON(ctx, "POST", "/api/v1/admin/integration-settings/mutate", request, &response); err != nil {
				return err
			}

			fmt.Printf("config %s: persisted=%t remote_persisted=%t restart_required=%t\n",
				path, response.Persisted, response.RemotePersisted, response.RestartRequired)
			if response.PersistError != "" {
				return fmt.Errorf("config not persisted: %s", response.PersistError)
			}
			return nil
		},
	}
}

// resolveConfigSetValue reads the value from --value, --value-file or stdin.
func resolveConfigSetValue(c *cli.Command) (string, error) {
	if c.Bool("value-stdin") {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed reading value from stdin: %w", err)
		}
		return strings.TrimRight(string(raw), "\n"), nil
	}
	if file := strings.TrimSpace(c.String("value-file")); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("failed reading value file %q: %w", file, err)
		}
		return strings.TrimRight(string(raw), "\n"), nil
	}
	return c.String("value"), nil
}
