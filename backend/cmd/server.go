package cmd

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/integrations"
	"backend/queue"
	"backend/runtimecfg"
	"backend/server"
	"backend/server/util"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/hibiken/asynqmon"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/bcrypt"
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
// - optional EXTRA_MODELS_JSON / --extra-models-json (path or inline JSON array)
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
		&cli.Uint16Flag{
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
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("CREATE_EXTRA_BOT"),
			Name:    "create-extra-bot",
			Usage:   "optional extra automated bot users in username:password format; can be repeated",
		},
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("ADD_BOT_FROM_CONFIG"),
			Name:    "add-bot-from-config",
			Usage:   "path(s) or inline JSON object/array defining additional bots; can be repeated",
		},
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("ADD_SSH_KEYS_FROM_CONFIG"),
			Name:    "add-ssh-keys-from-config",
			Usage:   "path(s) or inline JSON object/array defining SSH keys; can be repeated",
		},
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("ADD_SSH_SERVERS_FROM_CONFIG"),
			Name:    "add-ssh-servers-from-config",
			Usage:   "path(s) or inline JSON object/array defining SSH servers; can be repeated",
		},
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("ADD_SSH_KEY_GRANTS_FROM_CONFIG"),
			Name:    "add-ssh-key-grants-from-config",
			Usage:   "path(s) or inline JSON object/array defining SSH key grants; can be repeated",
		},
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("ADD_SSH_SERVER_GRANTS_FROM_CONFIG"),
			Name:    "add-ssh-server-grants-from-config",
			Usage:   "path(s) or inline JSON object/array defining SSH server grants; can be repeated",
		},
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("ADD_SSH_DEFAULT_OWNER"),
			Name:    "add-ssh-default-owner",
			Usage:   "default SSH bootstrap owner username/email/name; can be repeated",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("EXTRA_MODELS_JSON"),
			Name:    "extra-models-json",
			Usage:   "extra default models: filesystem path to a JSON array, or an inline JSON array string",
			Value:   "",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("FRONTEND_PROXY"),
			Name:    "frontend-proxy",
			Aliases: []string{"fpx"},
			Usage:   "Path '' for no proxy, e.g.: 'http://localhost:5173/' for remix",
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
		&cli.BoolFlag{
			Sources: cli.EnvVars("SIGNUP_REQUIRES_ADMIN_APPROVAL"),
			Name:    "signup-requires-admin-approval",
			Usage:   "Require admin approval for new user signup",
			Value:   false,
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

func normalizeSessionCookieDomain(host string) string {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]")
	if host == "" {
		return ""
	}

	if host == "0.0.0.0" || host == "::" || host == "localhost" {
		return ""
	}

	if ip := net.ParseIP(host); ip != nil {
		return ""
	}

	return host
}

type bootstrapUserSpec struct {
	Label                        string
	Credentials                  string
	IsAdmin                      bool
	IsAutomated                  bool
	SingletonAdmin               bool
	ValidateStrength             bool
	SuppressGeneratedPasswordLog bool
}

func nextAvailableUsername(DB *gorm.DB, base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "user"
	}
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s_%d", base, i+1)
		}
		var count int64
		if err := DB.Model(&database.User{}).Where("username = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("failed to resolve unique username for %q", base)
}

func nextAvailableEmail(DB *gorm.DB, localPart string) (string, error) {
	localPart = strings.TrimSpace(localPart)
	if localPart == "" {
		localPart = "user"
	}
	for i := 0; i < 1000; i++ {
		candidateLocal := localPart
		if i > 0 {
			candidateLocal = fmt.Sprintf("%s_%d", localPart, i+1)
		}
		candidate := candidateLocal + "@legacy.local"
		var count int64
		if err := DB.Model(&database.User{}).Where("email = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("failed to resolve unique email for %q", localPart)
}

func renameReservedAdminUsernameConflicts(DB *gorm.DB, excludeUserID uint) error {
	q := DB.Where("username = ?", "admin")
	if excludeUserID != 0 {
		q = q.Where("id <> ?", excludeUserID)
	}

	conflicts := []database.User{}
	if err := q.Find(&conflicts).Error; err != nil {
		return err
	}

	for _, conflict := range conflicts {
		// TODO: stop auto-renaming reserved "admin" username and fail startup after legacy deployments migrate.
		candidate, err := nextAvailableUsername(DB, fmt.Sprintf("admin_legacy_%d", conflict.ID))
		if err != nil {
			return err
		}
		if err := DB.Model(&database.User{}).Where("id = ?", conflict.ID).Update("username", candidate).Error; err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(conflict.Email), "admin") {
			emailCandidate, emailErr := nextAvailableEmail(DB, candidate)
			if emailErr != nil {
				return emailErr
			}
			if err := DB.Model(&database.User{}).Where("id = ?", conflict.ID).Update("email", emailCandidate).Error; err != nil {
				return err
			}
		}
	}

	return nil
}

func ensureSingletonAdminUser(DB *gorm.DB, password string, isAutomated bool) (*database.User, error) {
	if err := renameReservedAdminUsernameConflicts(DB, 0); err != nil {
		return nil, err
	}

	var admin database.User
	err := DB.First(&admin, "is_admin = ?", true).Error
	if err == nil {
		if err := renameReservedAdminUsernameConflicts(DB, admin.ID); err != nil {
			return nil, err
		}
		updates := map[string]interface{}{}
		if strings.TrimSpace(admin.Username) != "admin" {
			updates["username"] = "admin"
		}
		if strings.TrimSpace(admin.Name) == "" {
			updates["name"] = "admin"
		}
		if isAutomated && !admin.IsAutomated {
			updates["is_automated"] = true
		}
		if len(updates) > 0 {
			if err := DB.Model(&admin).Updates(updates).Error; err != nil {
				return nil, err
			}
			if reloadErr := DB.First(&admin, "id = ?", admin.ID).Error; reloadErr != nil {
				return nil, reloadErr
			}
		}
		return &admin, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	var passwordHash string
	if strings.HasPrefix(password, "hashed_") {
		passwordHash = strings.TrimPrefix(password, "hashed_")
	} else {
		hashedPassword, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			return nil, hashErr
		}
		passwordHash = string(hashedPassword)
	}

	adminEmail, emailErr := nextAvailableEmail(DB, "admin")
	if emailErr != nil {
		return nil, emailErr
	}

	admin = database.User{
		Name:         "admin",
		Username:     "admin",
		Email:        adminEmail,
		PasswordHash: passwordHash,
		ContactToken: uuid.NewString(),
		IsAdmin:      true,
		IsAutomated:  isAutomated,
	}
	if err := DB.Create(&admin).Error; err != nil {
		return nil, err
	}

	return &admin, nil
}

func resolveBootstrapPassword(rawPassword string, validateStrength bool, label string, suppressGeneratedPasswordLog bool) (string, error) {
	if rawPassword == "random" {
		generatedPassword, genErr := generateRandomPassword()
		if genErr != nil {
			return "", fmt.Errorf("failed to generate random password for %s: %w", label, genErr)
		}
		if !suppressGeneratedPasswordLog {
			fmt.Printf("Generated random password for %s: %s\n", label, generatedPassword)
			fmt.Println("IMPORTANT: Save this password securely; it will not be shown again.")
		}
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

	password, err := resolveBootstrapPassword(rawPassword, spec.ValidateStrength, spec.Label, spec.SuppressGeneratedPasswordLog)
	if err != nil {
		return nil, err
	}

	if spec.SingletonAdmin {
		return ensureSingletonAdminUser(DB, password, spec.IsAutomated)
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
		Action: func(ctx context.Context, c *cli.Command) error {
			integrations.EnsureLoaded()
			msgmate.EnsureExternalToolsRegistered()
			database.RegisterExternalModels(integrations.AdditionalModels()...)
			for _, migration := range integrations.AdditionalMigrations() {
				database.RegisterExternalMigrations(database.FunctionMigration{
					Name: migration.Name,
					Run:  migration.Run,
				})
			}

			runtimeValues := map[string]runtimecfg.Value{
				"DB_BACKEND":              {Value: c.String("db-backend"), Sensitive: false},
				"DB_PATH":                 {Value: c.String("db-path"), Sensitive: false},
				"DEBUG":                   {Value: fmt.Sprintf("%t", c.Bool("debug")), Sensitive: false},
				"SETUP_TEST_USERS":        {Value: fmt.Sprintf("%t", c.Bool("setup-test-users")), Sensitive: false},
				"RESET_DB":                {Value: fmt.Sprintf("%t", c.Bool("reset-db")), Sensitive: false},
				"HOST":                    {Value: c.String("host"), Sensitive: false},
				"PORT":                    {Value: fmt.Sprintf("%d", c.Uint16("port")), Sensitive: false},
				"ROOT_CREDENTIALS":        {Value: c.String("root-credentials"), Sensitive: true},
				"DEFAULT_BOT_CREDENTIALS": {Value: c.String("default-bot"), Sensitive: true},
				"CREATE_EXTRA_USER":       {Value: strings.Join(c.StringSlice("create-extra-user"), ","), Sensitive: true},
				"CREATE_EXTRA_BOT":        {Value: strings.Join(c.StringSlice("create-extra-bot"), ","), Sensitive: true},
				"ADD_BOT_FROM_CONFIG":     {Value: strings.Join(c.StringSlice("add-bot-from-config"), ","), Sensitive: false},
				"ADD_SSH_KEYS_FROM_CONFIG": {
					Value:     strings.Join(c.StringSlice("add-ssh-keys-from-config"), ","),
					Sensitive: true,
				},
				"ADD_SSH_SERVERS_FROM_CONFIG": {
					Value:     strings.Join(c.StringSlice("add-ssh-servers-from-config"), ","),
					Sensitive: true,
				},
				"ADD_SSH_KEY_GRANTS_FROM_CONFIG": {
					Value:     strings.Join(c.StringSlice("add-ssh-key-grants-from-config"), ","),
					Sensitive: false,
				},
				"ADD_SSH_SERVER_GRANTS_FROM_CONFIG": {
					Value:     strings.Join(c.StringSlice("add-ssh-server-grants-from-config"), ","),
					Sensitive: false,
				},
				"ADD_SSH_DEFAULT_OWNER": {
					Value:     strings.Join(c.StringSlice("add-ssh-default-owner"), ","),
					Sensitive: false,
				},
				"EXTRA_MODELS_JSON": {Value: c.String("extra-models-json"), Sensitive: false},
				"FRONTEND_PROXY":    {Value: c.String("frontend-proxy"), Sensitive: false},
				"START_WORKER":      {Value: fmt.Sprintf("%t", c.Bool("start-worker")), Sensitive: false},
				"ASYNQ_CONCURRENCY": {Value: fmt.Sprintf("%d", c.Int("asynq-concurrency")), Sensitive: false},
				"SIGNUP_REQUIRES_ADMIN_APPROVAL": {
					Value:     fmt.Sprintf("%t", c.Bool("signup-requires-admin-approval")),
					Sensitive: false,
				},
				"REDIS_URL":          {Value: c.String("redis-url"), Sensitive: true},
				"REDIS_MODE":         {Value: c.String("redis-mode"), Sensitive: false},
				"REDIS_ADDR":         {Value: c.String("redis-addr"), Sensitive: false},
				"REDIS_PASSWORD":     {Value: c.String("redis-password"), Sensitive: true},
				"REDIS_DB":           {Value: fmt.Sprintf("%d", c.Int("redis-db")), Sensitive: false},
				"OPENAI_API_KEY":     {Value: os.Getenv("OPENAI_API_KEY"), Sensitive: true},
				"ANTHROPIC_API_KEY":  {Value: os.Getenv("ANTHROPIC_API_KEY"), Sensitive: true},
				"ANTHROPIC_API_HOST": {Value: os.Getenv("ANTHROPIC_API_HOST"), Sensitive: true},
				"DEEPINFRA_API_KEY":  {Value: os.Getenv("DEEPINFRA_API_KEY"), Sensitive: true},
				"GROQ_API_KEY":       {Value: os.Getenv("GROQ_API_KEY"), Sensitive: true},
				"LITELLM_API_KEY":    {Value: os.Getenv("LITELLM_API_KEY"), Sensitive: true},
				"LITELLM_API_HOST":   {Value: os.Getenv("LITELLM_API_HOST"), Sensitive: true},
				"OPEN_CHAT_SEAL_KEY": {Value: os.Getenv("OPEN_CHAT_SEAL_KEY"), Sensitive: true},
				"MOBILE_ROUTE_API_WS_TO_UPSTREAM": {
					Value:     os.Getenv("MOBILE_ROUTE_API_WS_TO_UPSTREAM"),
					Sensitive: false,
				},
				"MOBILE_UPSTREAM_URL": {
					Value:     os.Getenv("MOBILE_UPSTREAM_URL"),
					Sensitive: false,
				},
				"MOBILE_API_CACHE_ENABLED": {
					Value:     os.Getenv("MOBILE_API_CACHE_ENABLED"),
					Sensitive: false,
				},
				"MOBILE_API_CACHE_TTL_SECONDS": {
					Value:     os.Getenv("MOBILE_API_CACHE_TTL_SECONDS"),
					Sensitive: false,
				},
				"MOBILE_API_CACHE_MAX_BODY_BYTES": {
					Value:     os.Getenv("MOBILE_API_CACHE_MAX_BODY_BYTES"),
					Sensitive: false,
				},
				"MOBILE_API_CACHE_MAX_ROWS": {
					Value:     os.Getenv("MOBILE_API_CACHE_MAX_ROWS"),
					Sensitive: false,
				},
			}

			for _, decl := range integrations.RuntimeEnvDeclarations() {
				if _, exists := runtimeValues[decl.Key]; exists {
					continue
				}
				runtimeValues[decl.Key] = runtimecfg.Value{
					Value:     os.Getenv(decl.Key),
					Sensitive: decl.Sensitive,
				}
			}

			runtimecfg.SetAll(runtimeValues)

			redisRuntime, err := resolveRedisRuntime(c)
			if err != nil {
				return err
			}
			defer redisRuntime.Cleanup()
			if redisRuntime.Mode == queue.RedisModeEmbedded {
				if redisRuntime.FallbackReason != nil {
					log.Printf("External redis unavailable (%v); started embedded redis at %s", redisRuntime.FallbackReason, redisRuntime.Address)
				} else {
					log.Printf("Started embedded redis at %s", redisRuntime.Address)
				}
			} else {
				log.Printf("Using external redis at %s", redisRuntime.Address)
			}

			queueClient := asynq.NewClient(redisRuntime.ConnOpt)
			defer queueClient.Close()

			queueInspector := asynq.NewInspector(redisRuntime.ConnOpt)
			asynqUIHandler := asynqmon.New(asynqmon.Options{
				RootPath:     "/admin/asynq/ui",
				RedisConnOpt: redisRuntime.ConnOpt,
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

			fullHost := fmt.Sprintf("http://%s:%d", c.String("host"), c.Uint16("port"))
			sessionCookieDomain := normalizeSessionCookieDomain(c.String("host"))

			// Initialize HTTP server and websocket handler.
			s, ch, _, err := server.BackendServer(
				DB,
				queueClient,
				queueInspector,
				asynqUIHandler,
				c.String("host"),
				c.Uint16("port"),
				c.Bool("debug"),
				c.String("frontend-proxy"),
				sessionCookieDomain,
				c.Bool("signup-requires-admin-approval"),
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

			for i, extra := range c.StringSlice("create-extra-bot") {
				if strings.TrimSpace(extra) == "" {
					continue
				}
				extraLabel := fmt.Sprintf("create-extra-bot[%d]", i)
				if _, err := ensureBootstrapUser(DB, bootstrapUserSpec{
					Label:            extraLabel,
					Credentials:      extra,
					IsAdmin:          false,
					IsAutomated:      true,
					ValidateStrength: !c.Bool("debug"),
				}); err != nil {
					return err
				}
			}

			openChatBootstrap := runtimecfg.GetOpenChatBootstrap()

			botSpecs := append([]string{}, c.StringSlice("add-bot-from-config")...)
			botSpecs = append(botSpecs, openChatBootstrap.BotSpecs...)
			if err := applyBotBootstrapConfigFiles(DB, botSpecs, !c.Bool("debug")); err != nil {
				return err
			}
			integrationBotDecls := integrations.BotBootstrapDeclarations()
			for _, decl := range integrationBotDecls {
				sourcePrefix := fmt.Sprintf("integration:%s.bot_bootstrap_configs[%d]", decl.IntegrationName, decl.Index)
				if err := applyIntegrationBotBootstrapConfigs(DB, sourcePrefix, []botBootstrapConfig{decl.Config}, !c.Bool("debug")); err != nil {
					return err
				}
			}

			sshDefaultOwners := append([]string{}, c.StringSlice("add-ssh-default-owner")...)
			sshDefaultOwners = append(sshDefaultOwners, openChatBootstrap.SSHDefaultOwners...)
			sshKeySpecs := append([]string{}, c.StringSlice("add-ssh-keys-from-config")...)
			sshKeySpecs = append(sshKeySpecs, openChatBootstrap.SSHKeySpecs...)
			sshServerSpecs := append([]string{}, c.StringSlice("add-ssh-servers-from-config")...)
			sshServerSpecs = append(sshServerSpecs, openChatBootstrap.SSHServerSpecs...)
			sshKeyGrantSpecs := append([]string{}, c.StringSlice("add-ssh-key-grants-from-config")...)
			sshKeyGrantSpecs = append(sshKeyGrantSpecs, openChatBootstrap.SSHKeyGrantSpecs...)
			sshServerGrantSpecs := append([]string{}, c.StringSlice("add-ssh-server-grants-from-config")...)
			sshServerGrantSpecs = append(sshServerGrantSpecs, openChatBootstrap.SSHServerGrantSpecs...)
			if err := applySSHBootstrapSources(DB, adminUser.Username, sshDefaultOwners, sshKeySpecs, sshServerSpecs, sshKeyGrantSpecs, sshServerGrantSpecs); err != nil {
				return err
			}

			providerSyncResult, err := database.SyncDefaultBotModelsByProviderKeys(DB, botUser.Name)
			if err != nil {
				return err
			}
			log.Printf(
				"Synced default bot provider-key model access bot=%s assigned=%d unassigned=%d skipped_unmanaged=%d skipped_invalid=%d",
				botUser.Name,
				providerSyncResult.Assigned,
				providerSyncResult.Unassigned,
				providerSyncResult.SkippedUnmanaged,
				providerSyncResult.SkippedInvalid,
			)

			if err := msgmate.SyncAutomatedBotProfiles(DB); err != nil {
				return err
			}
			if err := server.SetupBaseConnections(DB, adminUser.ID, botUser.ID); err != nil {
				return err
			}

			var workerServer *asynq.Server
			if c.Bool("start-worker") {
				workerServer = asynq.NewServer(
					redisRuntime.ConnOpt,
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
				if workerErr := workerServer.Start(processor.NewServeMux()); workerErr != nil {
					return fmt.Errorf("embedded asynq worker failed to start: %w", workerErr)
				}
				log.Printf("Started embedded asynq worker with concurrency=%d", c.Int("asynq-concurrency"))
			}

			serverErrCh := make(chan error, 1)
			go func() {
				err := s.ListenAndServe()
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					serverErrCh <- err
					return
				}
				serverErrCh <- nil
			}()

			signalCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stopSignals()

			select {
			case err := <-serverErrCh:
				if err != nil {
					if workerServer != nil {
						workerServer.Shutdown()
					}
					return err
				}
			case <-signalCtx.Done():
				log.Printf("Shutting down server (signal: %v)", signalCtx.Err())
				forceSigCh := make(chan os.Signal, 1)
				signal.Notify(forceSigCh, os.Interrupt)
				defer signal.Stop(forceSigCh)
				go func() {
					<-forceSigCh
					log.Printf("Received additional interrupt; forcing immediate exit")
					os.Exit(130)
				}()
				ch.Shutdown()
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancelShutdown()
				if err := s.Shutdown(shutdownCtx); err != nil {
					_ = s.Close()
					if workerServer != nil {
						workerServer.Shutdown()
					}
					return fmt.Errorf("server shutdown failed: %w", err)
				}
				if workerServer != nil {
					workerServer.Shutdown()
				}
				if err := <-serverErrCh; err != nil {
					return err
				}
			}

			return nil
		},
	}

	return cmd
}
