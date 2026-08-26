package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

type runVersionResponse struct {
	Version string `json:"version"`
}

type runLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type runBotLookup struct {
	Identifier   string
	ContactToken string
	Legacy       bool
}

type runBotsGetResponse struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type runContactsListResponse struct {
	Rows []runContactRow `json:"rows"`
}

type runContactRow struct {
	ContactToken string `json:"contact_token"`
	Name         string `json:"name"`
	UserUUID     string `json:"user_uuid"`
	IsAutomated  bool   `json:"is_automated"`
}

type runCreateInteractionRequest struct {
	Message string `json:"message"`
}

type runCreateInteractionResponse struct {
	ChatUUID string `json:"chat_uuid"`
}

type runLegacyCreateChatRequest struct {
	ContactToken string `json:"contact_token"`
	FirstMessage string `json:"first_message"`
	ChatType     string `json:"chat_type"`
}

type runLegacyCreateChatResponse struct {
	UUID string `json:"uuid"`
}

type runInteractionStatusResponse struct {
	IsActive              bool   `json:"is_active"`
	State                 string `json:"state"`
	LatestMessageFinished *bool  `json:"latest_message_finished"`
}

type runMessagesListResponse struct {
	Rows []runMessageRow `json:"rows"`
}

type runMessageRow struct {
	SenderIsAutomated bool   `json:"sender_is_automated"`
	Text              string `json:"text"`
	DataType          string `json:"data_type"`
}

type runHTTPClient struct {
	baseURL  string
	client   *http.Client
	apiToken string
}

func newRunHTTPClient(baseURL string, apiToken string, timeout time.Duration) (*runHTTPClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &runHTTPClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client: &http.Client{
			Timeout: timeout,
			Jar:     jar,
		},
		apiToken: strings.TrimSpace(apiToken),
	}, nil
}

func (c *runHTTPClient) requestJSON(ctx context.Context, method string, path string, requestBody interface{}, responseBody interface{}) (*http.Response, error) {
	fullURL := c.baseURL + path
	var bodyReader io.Reader
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return resp, fmt.Errorf("%s %s failed: %s", method, path, strings.TrimSpace(string(body)))
	}

	if responseBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
			return resp, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}

	return resp, nil
}

func (c *runHTTPClient) canReachServer(ctx context.Context) bool {
	var version runVersionResponse
	_, err := c.requestJSON(ctx, http.MethodGet, "/api/version", nil, &version)
	return err == nil
}

func resolveRunCredentials(c *cli.Command) (string, string, error) {
	username := strings.TrimSpace(c.String("username"))
	password := c.String("password")
	if username != "" || password != "" {
		if username == "" || password == "" {
			return "", "", fmt.Errorf("both --username and --password must be provided together")
		}
		return username, password, nil
	}

	rawRoot := strings.TrimSpace(os.Getenv("ROOT_CREDENTIALS"))
	if rawRoot != "" {
		u, p, err := parseCredentials(rawRoot, "ROOT_CREDENTIALS")
		if err == nil && p != "random" && !strings.HasPrefix(p, "hashed_") {
			return u, p, nil
		}
	}

	return "admin", "password", nil
}

func resolveGlobalConfigArgs(c *cli.Command) []string {
	args := make([]string, 0)
	if cfg := strings.TrimSpace(c.String("config")); cfg != "" {
		args = append(args, "--config", cfg)
	}
	if c.Bool("config-override-env") {
		args = append(args, "--config-override-env")
	}
	return args
}

func startTemporaryServer(ctx context.Context, c *cli.Command, host string, port uint16) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}

	args := resolveGlobalConfigArgs(c)
	args = append(args,
		"server",
		"--host", host,
		"--port", strconv.Itoa(int(port)),
	)

	proc := exec.CommandContext(ctx, exe, args...)
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	proc.Env = os.Environ()

	if err := proc.Start(); err != nil {
		return nil, err
	}

	return proc, nil
}

func stopTemporaryServer(proc *exec.Cmd) {
	if proc == nil || proc.Process == nil {
		return
	}
	_ = proc.Process.Kill()
	_, _ = proc.Process.Wait()
}

func waitForServerHealthy(ctx context.Context, api *runHTTPClient, proc *exec.Cmd, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if api.canReachServer(ctx) {
			return nil
		}
		if proc != nil && proc.ProcessState != nil && proc.ProcessState.Exited() {
			return fmt.Errorf("temporary server exited before becoming healthy")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("temporary server did not become healthy within %s", timeout)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func loginIfNeeded(ctx context.Context, c *runHTTPClient, username string, password string) error {
	if c.apiToken != "" {
		return nil
	}
	_, err := c.requestJSON(ctx, http.MethodPost, "/api/v1/user/login", runLoginRequest{
		Email:    username,
		Password: password,
	}, nil)
	if err != nil {
		return fmt.Errorf("login failed for user %q: %w", username, err)
	}
	return nil
}

func resolveBotForRun(ctx context.Context, c *runHTTPClient, identifier string) (runBotLookup, error) {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return runBotLookup{}, fmt.Errorf("bot identifier is required")
	}

	path := "/api/v1/bots/" + url.PathEscape(trimmed)
	var bot runBotsGetResponse
	if _, err := c.requestJSON(ctx, http.MethodGet, path, nil, &bot); err == nil {
		resolved := strings.TrimSpace(bot.UUID)
		if resolved == "" {
			resolved = strings.TrimSpace(bot.Name)
		}
		if resolved == "" {
			resolved = trimmed
		}
		return runBotLookup{Identifier: resolved, Legacy: false}, nil
	}

	page := 1
	for {
		contactsPath := fmt.Sprintf("/api/v1/contacts/list?page=%d&limit=100", page)
		var contacts runContactsListResponse
		if _, err := c.requestJSON(ctx, http.MethodGet, contactsPath, nil, &contacts); err != nil {
			return runBotLookup{}, fmt.Errorf("bot lookup failed and contacts fallback failed: %w", err)
		}
		if len(contacts.Rows) == 0 {
			break
		}
		for _, row := range contacts.Rows {
			if !row.IsAutomated {
				continue
			}
			if trimmed == strings.TrimSpace(row.UserUUID) || trimmed == strings.TrimSpace(row.Name) || trimmed == strings.TrimSpace(row.ContactToken) {
				return runBotLookup{ContactToken: row.ContactToken, Legacy: true}, nil
			}
		}
		if len(contacts.Rows) < 100 {
			break
		}
		page++
	}

	return runBotLookup{}, fmt.Errorf("bot not found: %s", identifier)
}

func createRunInteraction(ctx context.Context, c *runHTTPClient, lookup runBotLookup, message string) (string, error) {
	if lookup.Legacy {
		var legacy runLegacyCreateChatResponse
		_, err := c.requestJSON(ctx, http.MethodPost, "/api/v1/chats/create", runLegacyCreateChatRequest{
			ContactToken: lookup.ContactToken,
			FirstMessage: message,
			ChatType:     "interaction",
		}, &legacy)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(legacy.UUID) == "" {
			return "", fmt.Errorf("legacy interaction create returned empty chat uuid")
		}
		return legacy.UUID, nil
	}

	var interaction runCreateInteractionResponse
	path := "/api/v1/bots/" + url.PathEscape(lookup.Identifier) + "/interactions"
	_, err := c.requestJSON(ctx, http.MethodPost, path, runCreateInteractionRequest{Message: message}, &interaction)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(interaction.ChatUUID) == "" {
		return "", fmt.Errorf("create interaction returned empty chat uuid")
	}
	return interaction.ChatUUID, nil
}

func fetchLatestBotMessage(ctx context.Context, c *runHTTPClient, chatUUID string) (string, error) {
	path := "/api/v1/chats/" + url.PathEscape(chatUUID) + "/messages/list?page=1&limit=100"
	var messages runMessagesListResponse
	if _, err := c.requestJSON(ctx, http.MethodGet, path, nil, &messages); err != nil {
		return "", err
	}
	for _, row := range messages.Rows {
		if !row.SenderIsAutomated {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.DataType), "event") {
			continue
		}
		text := strings.TrimSpace(row.Text)
		if text != "" {
			return text, nil
		}
	}
	return "", nil
}

func waitForInteractionCompletion(ctx context.Context, c *runHTTPClient, chatUUID string, timeout time.Duration, pollInterval time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	latestBotText := ""

	for {
		if time.Now().After(deadline) {
			if latestBotText != "" {
				return latestBotText, nil
			}
			return "", fmt.Errorf("interaction did not complete within %s", timeout)
		}

		if text, err := fetchLatestBotMessage(ctx, c, chatUUID); err == nil && text != "" {
			latestBotText = text
		}

		statusPath := "/api/v1/chats/" + url.PathEscape(chatUUID) + "/status"
		var status runInteractionStatusResponse
		if _, err := c.requestJSON(ctx, http.MethodGet, statusPath, nil, &status); err != nil {
			return "", err
		}

		finished := false
		if status.LatestMessageFinished != nil && *status.LatestMessageFinished {
			finished = true
		}
		state := strings.ToLower(strings.TrimSpace(status.State))
		if state == "finished" || state == "failed" {
			finished = true
		}
		if finished && !status.IsActive {
			if latestBotText == "" {
				text, err := fetchLatestBotMessage(ctx, c, chatUUID)
				if err == nil {
					latestBotText = text
				}
			}
			return latestBotText, nil
		}

		time.Sleep(pollInterval)
	}
}

func RunCli() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "create a one-shot bot interaction and print the final bot message",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "host",
				Usage:   "backend host",
				Value:   "127.0.0.1",
				Sources: cli.EnvVars("HOST"),
			},
			&cli.Uint16Flag{
				Name:    "port",
				Usage:   "backend port",
				Value:   1984,
				Sources: cli.EnvVars("PORT"),
			},
			&cli.StringFlag{
				Name:    "bot",
				Usage:   "bot identifier (uuid or name)",
				Value:   "bot",
				Sources: cli.EnvVars("OPEN_CHAT_RUN_BOT"),
			},
			&cli.StringFlag{
				Name:     "message",
				Usage:    "message sent as first interaction prompt",
				Required: true,
				Sources:  cli.EnvVars("OPEN_CHAT_RUN_MESSAGE"),
			},
			&cli.StringFlag{
				Name:    "username",
				Usage:   "login username (required with --password when no --api-token is provided)",
				Sources: cli.EnvVars("OPEN_CHAT_RUN_USERNAME"),
			},
			&cli.StringFlag{
				Name:    "password",
				Usage:   "login password (required with --username when no --api-token is provided)",
				Sources: cli.EnvVars("OPEN_CHAT_RUN_PASSWORD"),
			},
			&cli.StringFlag{
				Name:    "api-token",
				Usage:   "access token used as Authorization: Bearer",
				Sources: cli.EnvVars("OPEN_CHAT_RUN_API_TOKEN"),
			},
			&cli.IntFlag{
				Name:    "timeout-seconds",
				Usage:   "max seconds to wait for interaction completion",
				Value:   120,
				Sources: cli.EnvVars("OPEN_CHAT_RUN_TIMEOUT_SECONDS"),
			},
			&cli.IntFlag{
				Name:    "poll-ms",
				Usage:   "polling interval in milliseconds",
				Value:   500,
				Sources: cli.EnvVars("OPEN_CHAT_RUN_POLL_MS"),
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			host := strings.TrimSpace(c.String("host"))
			if host == "" {
				return fmt.Errorf("host is required")
			}
			port := c.Uint16("port")
			baseURL := fmt.Sprintf("http://%s:%d", host, port)

			message := strings.TrimSpace(c.String("message"))
			if message == "" {
				return fmt.Errorf("message is required")
			}
			botIdentifier := strings.TrimSpace(c.String("bot"))
			if botIdentifier == "" {
				botIdentifier = "bot"
			}

			timeoutSeconds := c.Int("timeout-seconds")
			if timeoutSeconds <= 0 {
				timeoutSeconds = 120
			}
			pollMs := c.Int("poll-ms")
			if pollMs < 100 {
				pollMs = 100
			}

			api, err := newRunHTTPClient(baseURL, c.String("api-token"), 10*time.Second)
			if err != nil {
				return err
			}

			serverWasRunning := api.canReachServer(ctx)
			var tempServer *exec.Cmd
			if serverWasRunning {
				fmt.Printf("[open-chat run] Using existing server at %s\n", baseURL)
			} else {
				fmt.Printf("[open-chat run] Starting temporary server at %s\n", baseURL)
				tempServer, err = startTemporaryServer(ctx, c, host, port)
				if err != nil {
					return err
				}
				defer func() {
					stopTemporaryServer(tempServer)
					fmt.Printf("[open-chat run] Stopped temporary server at %s\n", baseURL)
				}()
				if err := waitForServerHealthy(ctx, api, tempServer, 30*time.Second); err != nil {
					return err
				}
			}

			username, password, err := resolveRunCredentials(c)
			if err != nil {
				return err
			}
			if err := loginIfNeeded(ctx, api, username, password); err != nil {
				return err
			}

			lookup, err := resolveBotForRun(ctx, api, botIdentifier)
			if err != nil {
				return err
			}
			chatUUID, err := createRunInteraction(ctx, api, lookup, message)
			if err != nil {
				return err
			}

			finalText, err := waitForInteractionCompletion(ctx, api, chatUUID, time.Duration(timeoutSeconds)*time.Second, time.Duration(pollMs)*time.Millisecond)
			if err != nil {
				return err
			}
			if strings.TrimSpace(finalText) == "" {
				fmt.Printf("[open-chat run] interaction %s completed without bot text output\n", chatUUID)
				return nil
			}

			fmt.Println(finalText)
			return nil
		},
	}
}
