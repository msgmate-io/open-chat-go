package cmd

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/queue"
	"backend/runtimecfg"
	"backend/server"
	"backend/server/util"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"strings"
	"unicode"

	"github.com/hibiken/asynq"
	"github.com/hibiken/asynqmon"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

// generateRandomPassword generates a secure random password with:
// - At least 16 characters
// - Contains uppercase and lowercase letters
// - Contains numbers
// - Contains special characters
func generateRandomPassword() (string, error) {
	const (
		lowercase = "abcdefghijklmnopqrstuvwxyz"
		uppercase = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		numbers   = "0123456789"
		special   = "!@#$%^&*()_+-=[]{}|;:,.<>?"
		allChars  = lowercase + uppercase + numbers + special
	)

	// Ensure at least one of each required character type
	password := make([]byte, 16)

	// Use crypto/rand for secure random selection
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	password[0] = lowercase[int(randomBytes[0])%len(lowercase)]
	password[1] = uppercase[int(randomBytes[1])%len(uppercase)]
	password[2] = numbers[int(randomBytes[2])%len(numbers)]
	password[3] = special[int(randomBytes[3])%len(special)]

	// Fill the rest randomly
	for i := 4; i < 16; i++ {
		password[i] = allChars[int(randomBytes[i])%len(allChars)]
	}

	// Shuffle the password to avoid predictable patterns
	shuffleBytes := make([]byte, 16)
	if _, err := rand.Read(shuffleBytes); err != nil {
		return "", err
	}
	for i := len(password) - 1; i > 0; i-- {
		j := int(shuffleBytes[i]) % (i + 1)
		password[i], password[j] = password[j], password[i]
	}

	return string(password), nil
}

// validatePasswordStrength validates that a password meets security requirements:
// - At least 8 characters long
// - Contains letters and numbers
// - Contains at least one special character
func validatePasswordStrength(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters long")
	}

	hasLetter := false
	hasNumber := false
	hasSpecial := false

	for _, char := range password {
		switch {
		case unicode.IsLetter(char):
			hasLetter = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	if !hasLetter {
		return fmt.Errorf("password must contain at least one letter")
	}
	if !hasNumber {
		return fmt.Errorf("password must contain at least one number")
	}
	if !hasSpecial {
		return fmt.Errorf("password must contain at least one special character")
	}

	return nil
}

// @doc:open-chat-server-command-options
// The `open-chat server` command controls API startup, database configuration,
// bootstrap credentials, frontend proxying, and embedded Asynq worker behavior.
//
// Runtime behavior is driven by CLI flags and environment variables:
// - DB backend/path and debug/reset toggles
// - host/port binding and root/default bot bootstrap credentials
// - Redis connection options used by Asynq and Asynqmon
// - optional embedded worker via START_WORKER and ASYNQ_CONCURRENCY
func GetServerFlags() []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{
			Sources: cli.EnvVars("DB_BACKEND"),
			Name:    "db-backend",
			Aliases: []string{"db"},
			Value:   "sqlite",
			Usage:   "database driver to use",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("DB_PATH"),
			Name:    "db-path",
			Aliases: []string{"dp"},
			Value:   "data.db",
			Usage:   "For sqlite the path to the database file",
		},
		&cli.BoolFlag{
			Sources: cli.EnvVars("DEBUG"),
			Name:    "debug",
			Aliases: []string{"d"},
			Value:   true, // TODO default to false
			Usage:   "enable debug mode",
		},
		&cli.BoolFlag{
			Sources: cli.EnvVars("SETUP_TEST_USERS"),
			Name:    "setup-test-users",
			Aliases: []string{"stu"},
			Value:   false,
			Usage:   "setup test users",
		},
		&cli.BoolFlag{
			Sources: cli.EnvVars("RESET_DB"),
			Name:    "reset-db",
			Aliases: []string{"rdb"},
			Value:   false,
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("HOST"),
			Name:    "host",
			Aliases: []string{"b"},
			Value:   "127.0.0.1",
			Usage:   "server bind address",
		},
		&cli.IntFlag{
			Sources: cli.EnvVars("PORT"),
			Name:    "port",
			Aliases: []string{"p"},
			Value:   1984,
			Usage:   "server port",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("ROOT_CREDENTIALS"),
			Name:    "root-credentials",
			Aliases: []string{"rc"},
			Usage:   "root credentials",
			Value:   "admin:random",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("DEFAULT_BOT_CREDENTIALS"),
			Name:    "default-bot",
			Aliases: []string{"botc"},
			Usage:   "bot login credentials",
			Value:   GetBuildTimeDefaultBot(),
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("FRONTEND_PROXY"),
			Name:    "frontend-proxy",
			Aliases: []string{"fpx"},
			Usage:   "Path '' for no proxy, e.g.: 'http://localhost:5173/' for remix",
			Value:   "",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("STORYBOOK_FRONTEND_PROXY"),
			Name:    "storybook-frontend-proxy",
			Aliases: []string{"sbpx"},
			Usage:   "Dev-only: proxy a Storybook dev server under /storybook, e.g.: 'http://storybook:6006'",
			Value:   "",
		},
		&cli.BoolFlag{
			Sources: cli.EnvVars("START_WORKER"),
			Name:    "start-worker",
			Aliases: []string{"sw"},
			Value:   true,
			Usage:   "Start embedded asynq worker in server process",
		},
		&cli.IntFlag{
			Sources: cli.EnvVars("ASYNQ_CONCURRENCY"),
			Name:    "asynq-concurrency",
			Usage:   "Number of concurrent worker goroutines",
			Value:   10,
		},
	}

	flags = append(flags, GetRedisFlags()...)
	return flags
}

func parseCredentials(raw, label string) (string, string, error) {
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%s must be in format username:password", label)
	}
	return parts[0], parts[1], nil
}

func ensureRootUser(DB *gorm.DB, username, password string, debug bool) (*database.User, error) {
	var (
		err  error
		user *database.User
	)

	if password == "random" {
		generatedPassword, genErr := generateRandomPassword()
		if genErr != nil {
			return nil, fmt.Errorf("failed to generate random password: %w", genErr)
		}
		password = generatedPassword
		fmt.Printf("Generated random root password: %s\n", password)
		fmt.Println("IMPORTANT: Save this password securely; it will not be shown again.")
	} else if !debug {
		if err := validatePasswordStrength(password); err != nil {
			return nil, fmt.Errorf("password does not meet security requirements: %w", err)
		}
	}

	// Hashed passwords always pass the strength validation due to prefix.
	if strings.HasPrefix(password, "hashed_") {
		hashedPassword := strings.TrimPrefix(password, "hashed_")
		err, user = util.CreateUserPwPreHashed(DB, username, hashedPassword, true)
	} else {
		err, user = util.CreateRootUser(DB, username, password)
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func ensureDefaultBotUser(DB *gorm.DB, username, password string) (*database.User, error) {
	err, botUser := util.CreateUser(DB, username, password, false)
	if err != nil {
		return nil, err
	}
	botUser.IsAutomated = true
	DB.Save(&botUser)
	if err := msgmate.CreateOrUpdateBotProfile(DB, *botUser); err != nil {
		return nil, err
	}
	return botUser, nil
}

func ServerCli() *cli.Command {
	cmd := &cli.Command{
		Name:  "server",
		Usage: "start the Open Chat server",
		Flags: GetServerFlags(),
		Action: func(_ context.Context, c *cli.Command) error {
			runtimecfg.SetAll(map[string]runtimecfg.Value{
				"DB_BACKEND":               {Value: c.String("db-backend"), Sensitive: false},
				"DB_PATH":                  {Value: c.String("db-path"), Sensitive: false},
				"DEBUG":                    {Value: fmt.Sprintf("%t", c.Bool("debug")), Sensitive: false},
				"SETUP_TEST_USERS":         {Value: fmt.Sprintf("%t", c.Bool("setup-test-users")), Sensitive: false},
				"RESET_DB":                 {Value: fmt.Sprintf("%t", c.Bool("reset-db")), Sensitive: false},
				"HOST":                     {Value: c.String("host"), Sensitive: false},
				"PORT":                     {Value: fmt.Sprintf("%d", c.Int("port")), Sensitive: false},
				"ROOT_CREDENTIALS":         {Value: c.String("root-credentials"), Sensitive: true},
				"DEFAULT_BOT_CREDENTIALS":  {Value: c.String("default-bot"), Sensitive: true},
				"FRONTEND_PROXY":           {Value: c.String("frontend-proxy"), Sensitive: false},
				"STORYBOOK_FRONTEND_PROXY": {Value: c.String("storybook-frontend-proxy"), Sensitive: false},
				"START_WORKER":             {Value: fmt.Sprintf("%t", c.Bool("start-worker")), Sensitive: false},
				"ASYNQ_CONCURRENCY":        {Value: fmt.Sprintf("%d", c.Int("asynq-concurrency")), Sensitive: false},
				"REDIS_URL":                {Value: c.String("redis-url"), Sensitive: true},
				"REDIS_ADDR":               {Value: c.String("redis-addr"), Sensitive: false},
				"REDIS_PASSWORD":           {Value: c.String("redis-password"), Sensitive: true},
				"REDIS_DB":                 {Value: fmt.Sprintf("%d", c.Int("redis-db")), Sensitive: false},
				"OPENAI_API_KEY":           {Value: os.Getenv("OPENAI_API_KEY"), Sensitive: true},
				"DEEPINFRA_API_KEY":        {Value: os.Getenv("DEEPINFRA_API_KEY"), Sensitive: true},
				"GROQ_API_KEY":             {Value: os.Getenv("GROQ_API_KEY"), Sensitive: true},
				"OPEN_CHAT_SEAL_KEY":       {Value: os.Getenv("OPEN_CHAT_SEAL_KEY"), Sensitive: true},
			})

			redisConnOpt, err := resolveRedisConnOpt(c)
			if err != nil {
				return err
			}

			queueClient := asynq.NewClient(redisConnOpt)
			defer queueClient.Close()

			queueInspector := asynq.NewInspector(redisConnOpt)
			asynqUIHandler := asynqmon.New(asynqmon.Options{
				RootPath:     "/admin/asynq/ui",
				RedisConnOpt: redisConnOpt,
				ReadOnly:     false,
			})
			defer asynqUIHandler.Close()

			DB := database.SetupDatabase(database.DBConfig{
				Backend:  c.String("db-backend"),
				FilePath: c.String("db-path"),
				Debug:    c.Bool("debug"),
				ResetDB:  c.Bool("reset-db"),
			})

			if c.Bool("setup-test-users") {
				database.SetupTestUsers(DB)
			}

			fullHost := fmt.Sprintf("http://%s:%d", c.String("host"), c.Int("port"))

			// Initialize HTTP server and websocket handler.
			s, ch, _, err := server.BackendServer(
				DB,
				queueClient,
				queueInspector,
				asynqUIHandler,
				c.String("host"),
				c.Int("port"),
				c.Bool("debug"),
				c.String("frontend-proxy"),
				c.String("storybook-frontend-proxy"),
				c.String("host"),
			)
			if err != nil {
				return err
			}

			fmt.Printf("Starting server on %s\n", fullHost)
			fmt.Printf("Find API reference at %s/reference\n", fullHost)

			rootUsername, rootPassword, err := parseCredentials(c.String("root-credentials"), "root-credentials")
			if err != nil {
				return err
			}

			adminUser, err := ensureRootUser(DB, rootUsername, rootPassword, c.Bool("debug"))
			if err != nil {
				return err
			}

			botUsername, botPassword, err := parseCredentials(c.String("default-bot"), "default-bot")
			if err != nil {
				return err
			}
			botUser, err := ensureDefaultBotUser(DB, botUsername, botPassword)
			if err != nil {
				return err
			}

			if err := server.SetupBaseConnections(DB, adminUser.ID, botUser.ID); err != nil {
				return err
			}

			if c.Bool("start-worker") {
				workerServer := asynq.NewServer(
					redisConnOpt,
					asynq.Config{
						Concurrency: int(c.Int("asynq-concurrency")),
						Queues: map[string]int{
							queue.QueueDefault: 1,
						},
					},
				)

				processor := &queue.Processor{
					DB:          DB,
					BackendHost: fullHost,
					WSHandler:   ch,
				}
				go func() {
					log.Printf("Starting embedded asynq worker with concurrency=%d", c.Int("asynq-concurrency"))
					if workerErr := workerServer.Run(processor.NewServeMux()); workerErr != nil {
						log.Printf("Embedded asynq worker failed: %v", workerErr)
					}
				}()
			}

			if err := s.ListenAndServe(); err != nil {
				return err
			}

			return nil
		},
	}

	return cmd
}
