package cmd

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/scheduler"
	"backend/server"
	"backend/server/util"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/urfave/cli/v3"
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
		&cli.BoolFlag{
			Sources: cli.EnvVars("SSL"),
			Name:    "ssl",
			Aliases: []string{"s"},
			Value:   false,
			Usage:   "enable ssl",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("TLS_KEY_PREFIX"),
			Name:    "tls-key-prefix",
			Aliases: []string{"tkp"},
			Value:   "",
			Usage:   "prefix for the TLS certificates",
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
		&cli.StringFlag{
			Sources: cli.EnvVars("HOST_DOMAIN"),
			Name:    "host-domain",
			Aliases: []string{"hd"},
			Value:   "localhost",
			Usage:   "domain for the host",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("HTTP_REDIRECT_PORT"),
			Name:    "http-redirect-port",
			Aliases: []string{"hrp"},
			Value:   "",
			Usage:   "port for the http redirect",
		},
		&cli.BoolFlag{
			Sources: cli.EnvVars("HTTP_FALLBACK_ENABLED"),
			Name:    "http-fallback-enabled",
			Aliases: []string{"hfe"},
			Value:   true,
			Usage:   "enable HTTP fallback server when TLS is enabled (for local access)",
		},
		&cli.IntFlag{
			Sources: cli.EnvVars("HTTP_FALLBACK_PORT"),
			Name:    "http-fallback-port",
			Aliases: []string{"hfp"},
			Value:   0,
			Usage:   "port for HTTP fallback server (0 = auto, uses main port + 1)",
		},
	}
}

func ServerCli() *cli.Command {
	log.Println("Hello from server cli")
	cmd := &cli.Command{
		Name:  "boom",
		Usage: "make an explosive entrance",
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

			// start channels to other nodes
			var fallbackPort int
			if c.Bool("http-fallback-enabled") {
				fallbackPort = int(c.Int("port")) + 1
				if c.Int("http-fallback-port") != 0 {
					fallbackPort = int(c.Int("http-fallback-port"))
				}
			}
			// First, determine both external and local fullHost values
			var externalFullHost string
			if c.Bool("ssl") {
				externalFullHost = fmt.Sprintf("https://%s", c.String("host-domain"))
				if c.Int("port") != 443 {
					externalFullHost = fmt.Sprintf("%s:%d", externalFullHost, c.Int("port"))
				}
			} else {
				externalFullHost = fmt.Sprintf("http://%s:%d", c.String("host"), c.Int("port"))
			}

			// Local fullHost should use fallback when available (SSL + fallback)
			localFullHost := externalFullHost
			if c.Bool("ssl") && c.Bool("http-fallback-enabled") {
				localFullHost = fmt.Sprintf("http://localhost:%d", fallbackPort)
			}

			// Initialize the scheduler service with the localFullHost (so it always reaches the node even if TLS is broken)
			schedulerService := scheduler.NewSchedulerService(DB, localFullHost)
			schedulerService.RegisterTasks()
			schedulerService.Start()
			defer schedulerService.Stop()

			// Pass external settings to BackendServer (serves HTTPS if enabled)
			s, ch, signalService, matrixService, _, err := server.BackendServer(
				DB,
				schedulerService,
				c.String("host"),
				c.Int("port"),
				c.Bool("debug"),
				c.Bool("ssl"),
				c.String("tls-key-prefix"),
				c.String("frontend-proxy"),
				c.String("host-domain"),
			)
			if err != nil {
				return err
			}

			fmt.Printf("Starting server on %s\n", externalFullHost)
			fmt.Printf("Find API reference at %s/reference\n", externalFullHost)

			rootCredentials := strings.Split(c.String("root-credentials"), ":")
			username := rootCredentials[0]
			password := rootCredentials[1]
			var adminUser *database.User

			// Handle random password generation
			if password == "random" {
				generatedPassword, err := generateRandomPassword()
				if err != nil {
					return fmt.Errorf("failed to generate random password: %w", err)
				}
				password = generatedPassword
				fmt.Printf("Generated random root password: %s\n", password)
				fmt.Println("⚠️  IMPORTANT: Save this password securely! It will not be shown again.")
			} else if !c.Bool("debug") {
				// Validate password strength if not in debug mode
				if err := validatePasswordStrength(password); err != nil {
					return fmt.Errorf("password does not meet security requirements: %w", err)
				}
			}

			// hashed passwords always pass the strengh validation anyways due to the prefix
			if strings.HasPrefix(password, "hashed_") {
				hashedPassword := strings.TrimPrefix(password, "hashed_")
				// instead of providing the plain text password one may also provide a pre-hashed password
				err, adminUser = util.CreateUserPwPreHashed(DB, username, hashedPassword, true)
			} else {
				err, adminUser = util.CreateRootUser(DB, username, password)
			}

			if err != nil {
				return err
			}

			// Create the default msgmate-io bot
			botCredentials := strings.Split(c.String("default-bot"), ":")
			usernameBot := botCredentials[0]
			passwordBot := botCredentials[1]

			err, botUser := util.CreateUser(DB, usernameBot, passwordBot, false)
			if err != nil {
				return err
			}
			DB.Save(&botUser)

			err = msgmate.CreateOrUpdateBotProfile(DB, *botUser)
			if err != nil {
				return err
			}

			// Create default connection with admin user
			err = server.SetupBaseConnections(DB, adminUser.ID, botUser.ID)
			if err != nil {
				return err
			}

			if c.Bool("start-bot") {
				go func() {
					time.Sleep(1 * time.Second)
					log.Printf("Starting bot with restart capability...")
					// Use localFullHost so bot login works via HTTP fallback if TLS is expired
					msgmate.StartBotWithRestart(localFullHost, ch, usernameBot, passwordBot)
				}()
			}

			if c.String("http-redirect-port") != "" {
				// Start HTTP redirect server
				protocol := "http"
				if c.Bool("ssl") {
					protocol = "https"
				}
				go func() {
					redirectMux := http.NewServeMux()
					redirectMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
						// Preserve the original domain from the request instead of always using host-domain
						host := r.Host
						if i := strings.Index(host, ":"); i != -1 {
							host = host[:i] // Remove port if present
						}

						target := fmt.Sprintf("%s://%s", protocol, host)
						if c.Int("port") != 443 {
							target += fmt.Sprintf(":%d", c.Int("port"))
						}
						target += r.URL.Path
						if r.URL.RawQuery != "" {
							target += "?" + r.URL.RawQuery
						}

						log.Printf("HTTP to HTTPS redirect: %s -> %s", r.URL.String(), target)
						http.Redirect(w, r, target, http.StatusMovedPermanently)
					})
					redirectServer := &http.Server{
						Addr:    ":" + c.String("http-redirect-port"),
						Handler: redirectMux,
					}
					if err := redirectServer.ListenAndServe(); err != nil {
						log.Printf("HTTP redirect server error: %v", err)
					}
				}()
			}

			if signalService != nil {
				log.Println("Starting all active Signal integrations...")
				signalService.StartAllActiveIntegrations()
			} else {
				log.Println("No Signal integration service found")
			}

			if matrixService != nil {
				log.Println("Starting all active Matrix integrations...")
				matrixService.StartAllActiveIntegrations()
			} else {
				log.Println("No Matrix integration service found")
			}

			// Start HTTP fallback server when TLS is enabled for local access
			if c.Bool("ssl") && c.Bool("http-fallback-enabled") {
				fallbackPort := c.Int("port") + 1
				if c.Int("http-fallback-port") != 0 {
					fallbackPort = c.Int("http-fallback-port")
				}
				log.Println("Starting HTTP fallback server for local access on port", fallbackPort)
				httpFallbackServer, err := server.CreateHTTPFallbackServer(
					DB,
					schedulerService,
					"localhost",
					int64(fallbackPort),
					c.Bool("debug"),
					c.String("frontend-proxy"),
					c.String("host-domain"),
					externalFullHost, // Pass main server URL for signalService
				)
				if err != nil {
					log.Printf("Warning: Failed to create HTTP fallback server: %v", err)
				} else {
					go func() {
						if err := httpFallbackServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
							log.Printf("HTTP fallback server error: %v", err)
						}
					}()
					log.Printf("HTTP fallback server started on localhost:%d", fallbackPort)
				}
			}

			if c.Bool("ssl") {
				err = s.ListenAndServeTLS("", "")
			} else {
				err = s.ListenAndServe()
			}

			if err != nil {
				return err
			}

			return nil
		},
	}

	return cmd
}
