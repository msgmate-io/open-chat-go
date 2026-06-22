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
// - host/port binding and bootstrap credentials for root, bot, and extra users
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
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("CREATE_EXTRA_USER"),
			Name:    "create-extra-user",
			Usage:   "optional extra users in username:password format; can be repeated",
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

type bootstrapUserSpec struct {
	Label            string
	Credentials      string
	IsAdmin          bool
	IsAutomated      bool
	SingletonAdmin   bool
	ValidateStrength bool
}

func resolveBootstrapPassword(rawPassword string, validateStrength bool, label string) (string, error) {
	if rawPassword == "random" {
		generatedPassword, genErr := generateRandomPassword()
		if genErr != nil {
			return "", fmt.Errorf("failed to generate random password for %s: %w", label, genErr)
		}
		fmt.Printf("Generated random password for %s: %s\n", label, generatedPassword)
		fmt.Println("IMPORTANT: Save this password securely; it will not be shown again.")
		return generatedPassword, nil
	}

	if strings.HasPrefix(rawPassword, "hashed_") {
		return rawPassword, nil
	}

	if validateStrength {
		if err := validatePasswordStrength(rawPassword); err != nil {
			return "", fmt.Errorf("password for %s does not meet security requirements: %w", label, err)
		}
	}

	return rawPassword, nil
}

func ensureBootstrapUser(DB *gorm.DB, spec bootstrapUserSpec) (*database.User, error) {
	username, rawPassword, err := parseCredentials(spec.Credentials, spec.Label)
	if err != nil {
		return nil, err
	}

	password, err := resolveBootstrapPassword(rawPassword, spec.ValidateStrength, spec.Label)
	if err != nil {
		return nil, err
	}

	if spec.SingletonAdmin {
		var existingAdmin database.User
		q := DB.First(&existingAdmin, "is_admin = ?", true)
		if q.Error == nil {
			if spec.IsAutomated && !existingAdmin.IsAutomated {
				existingAdmin.IsAutomated = true
				DB.Save(&existingAdmin)
			}
			return &existingAdmin, nil
		}
		if q.Error != nil && q.Error != gorm.ErrRecordNotFound {
			return nil, q.Error
		}
	}

	var user *database.User
	if strings.HasPrefix(password, "hashed_") {
		hashedPassword := strings.TrimPrefix(password, "hashed_")
		err, user = util.CreateUserPwPreHashed(DB, username, hashedPassword, spec.IsAdmin)
	} else {
		err, user = util.CreateUser(DB, username, password, spec.IsAdmin)
	}
	if err != nil {
		return nil, err
	}

	if spec.IsAutomated && user != nil && !user.IsAutomated {
		user.IsAutomated = true
		DB.Save(user)
	}

	return user, nil
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
				"CREATE_EXTRA_USER":        {Value: strings.Join(c.StringSlice("create-extra-user"), ","), Sensitive: true},
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
				"LITELLM_API_KEY":          {Value: os.Getenv("LITELLM_API_KEY"), Sensitive: true},
				"LITELLM_API_HOST":         {Value: os.Getenv("LITELLM_API_HOST"), Sensitive: true},
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

			if err := database.SeedModelConfigs(DB); err != nil {
				return err
			}

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

			adminUser, err := ensureBootstrapUser(DB, bootstrapUserSpec{
				Label:            "root-credentials",
				Credentials:      c.String("root-credentials"),
				IsAdmin:          true,
				SingletonAdmin:   true,
				ValidateStrength: !c.Bool("debug"),
			})
			if err != nil {
				return err
			}

			botUser, err := ensureBootstrapUser(DB, bootstrapUserSpec{
				Label:            "default-bot",
				Credentials:      c.String("default-bot"),
				IsAdmin:          false,
				IsAutomated:      true,
				ValidateStrength: !c.Bool("debug"),
			})
			if err != nil {
				return err
			}

			for i, extra := range c.StringSlice("create-extra-user") {
				if strings.TrimSpace(extra) == "" {
					continue
				}
				extraLabel := fmt.Sprintf("create-extra-user[%d]", i)
				if _, err := ensureBootstrapUser(DB, bootstrapUserSpec{
					Label:            extraLabel,
					Credentials:      extra,
					IsAdmin:          false,
					ValidateStrength: !c.Bool("debug"),
				}); err != nil {
					return err
				}
			}

			if err := msgmate.SyncAutomatedBotProfiles(DB); err != nil {
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
