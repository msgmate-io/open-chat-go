package cmd

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server"
	"backend/server/util"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

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

func GetServerFlags() []cli.Flag {
	return []cli.Flag{
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
		&cli.BoolFlag{
			Sources: cli.EnvVars("START_BOT"),
			Name:    "start-bot",
			Aliases: []string{"sb"},
			Value:   true,
			Usage:   "If the in-build msgmate bot should be started",
		},
	}
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
				c.String("host"),
				c.Int("port"),
				c.Bool("debug"),
				c.String("frontend-proxy"),
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

			if c.Bool("start-bot") {
				go func() {
					time.Sleep(1 * time.Second)
					log.Printf("Starting bot with restart capability...")
					msgmate.StartBotWithRestart(fullHost, ch, botUsername, botPassword)
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
