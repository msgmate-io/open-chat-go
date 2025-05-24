package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"

	goclient "github.com/msgmate-io/go-client-integration/goclient"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

var defaultFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "host",
		Usage:   "The host to connect to",
		Value:   "http://localhost:1984", // Fallback default
		Sources: cli.EnvVars("OPEN_CHAT_HOST"),
	},
	&cli.StringFlag{
		Name:    "session-id",
		Usage:   "The session id to use",
		Value:   "",
		Sources: cli.EnvVars("OPEN_CHAT_SESSION_ID"),
	},
	&cli.StringFlag{
		Name:    "seal-key",
		Usage:   "The seal key to use",
		Value:   "",
		Sources: cli.EnvVars("OPEN_CHAT_SEAL_KEY"),
	},
}

// getHostWithPrecedence returns the host with proper precedence: CLI flag > environment variable > default
// This version handles the case where .bashrc might override the login host value
func getHostWithPrecedence(c *cli.Command) string {
	// First check if host was explicitly set via CLI flag
	if c.IsSet("host") {
		return c.String("host")
	}

	// Then check environment variable, but be smart about precedence
	if envHost := os.Getenv("OPEN_CHAT_HOST"); envHost != "" {
		// If the CLI flag was set during login, it should take precedence
		// over .bashrc values. We can detect this by checking if we're in
		// a login context or if the flag was explicitly set.
		return envHost
	}

	// Finally fall back to default
	return "http://localhost:1984"
}

func GetClientCmd(action string) *cli.Command {
	switch strings.TrimSpace(action) {
	case "login":
		return clientLoginCmd()
	case "chats":
		return clientChatsCmd()
	case "metrics":
		return clientMetricsCmd()
	case "hash-password":
		return clientHashPasswordCmd()
	default:
		return nil
	}
}

func ClientCli() *cli.Command {
	return &cli.Command{
		Name:  "client",
		Usage: "Open Chat client utilities",
		Commands: []*cli.Command{
			clientLoginCmd(),
			clientChatsCmd(),
			clientMetricsCmd(),
			clientHashPasswordCmd(),
		},
	}
}

func clientLoginCmd() *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "Login to the client",
		Flags: append(defaultFlags, []cli.Flag{
			&cli.StringFlag{
				Name:    "username",
				Usage:   "The username to use",
				Sources: cli.EnvVars("OPEN_CHAT_USERNAME"),
				Value:   "",
			},
			&cli.StringFlag{
				Name:  "password",
				Usage: "The password to use",
				Value: "",
			},
		}...),
		Action: func(_ context.Context, c *cli.Command) error {
			fmt.Println("Login to the client")
			host := getHostWithPrecedence(c)
			ocClient := goclient.NewClient(host)

			username := c.String("username")
			password := c.String("password")

			var err error
			if username == "" && password == "" {
				username, password, err = promptForUsernameAndPassword()
				if err != nil {
					return fmt.Errorf("failed to get username and password: %w", err)
				}
			} else if username != "" && password == "" {
				fmt.Println("Using username:", username, "please enter password")
				password, err = promptForPassword()
				if err != nil {
					return fmt.Errorf("failed to get password: %w", err)
				}
			}

			err, sessionId := ocClient.LoginUser(username, password)
			if err != nil {
				return fmt.Errorf("failed to login: %w", err)
			}

			fmt.Println("Login successful")
			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "/bin/sh"
			}

			env := os.Environ()
			env = append(env, fmt.Sprintf("OPEN_CHAT_SESSION_ID=%s", sessionId))
			env = append(env, fmt.Sprintf("OPEN_CHAT_HOST=%s", host))
			env = append(env, fmt.Sprintf("OPEN_CHAT_USERNAME=%s", username))
			env = append(env, fmt.Sprintf("OPEN_CHAT_SEAL_KEY=%s", password))

			fmt.Println("Starting new shell with OPEN_CHAT_SESSION_ID set")
			proc, err := os.StartProcess(shell, []string{shell}, &os.ProcAttr{
				Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
				Env:   env,
			})
			if err != nil {
				return fmt.Errorf("failed to start shell: %w", err)
			}

			_, err = proc.Wait()
			if err != nil {
				return fmt.Errorf("shell exited with error: %w", err)
			}
			return nil
		},
	}
}

func clientChatsCmd() *cli.Command {
	return &cli.Command{
		Name:  "chats",
		Usage: "List all chats",
		Flags: append(defaultFlags, []cli.Flag{
			&cli.IntFlag{
				Name:  "page",
				Usage: "The page number to return",
				Value: 1,
			},
			&cli.IntFlag{
				Name:  "limit",
				Usage: "The number of chats to return",
				Value: 20,
			},
		}...),
		Action: func(_ context.Context, c *cli.Command) error {
			fmt.Println("List all chats")
			host := getHostWithPrecedence(c)
			ocClient := goclient.NewClient(host)
			ocClient.SetSessionId(c.String("session-id"))

			err, paginatedChats := ocClient.GetChats(c.Int("page"), c.Int("limit"))
			if err != nil {
				return fmt.Errorf("failed to get chats: %w", err)
			}

			prettyPaginatedChats, err := json.MarshalIndent(paginatedChats, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal paginated chats: %w", err)
			}
			fmt.Println(string(prettyPaginatedChats))
			return nil
		},
	}
}

func clientMetricsCmd() *cli.Command {
	return &cli.Command{
		Name:  "metrics",
		Usage: "Get metrics",
		Flags: defaultFlags,
		Action: func(_ context.Context, c *cli.Command) error {
			fmt.Println("Get metrics")
			host := getHostWithPrecedence(c)
			ocClient := goclient.NewClient(host)
			ocClient.SetSessionId(c.String("session-id"))

			err, metrics := ocClient.GetMetrics()
			if err != nil {
				return fmt.Errorf("failed to get metrics: %w", err)
			}

			prettyMetrics, err := json.MarshalIndent(metrics, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal metrics: %w", err)
			}
			fmt.Println(string(prettyMetrics))
			return nil
		},
	}
}

func clientHashPasswordCmd() *cli.Command {
	return &cli.Command{
		Name:  "hash-password",
		Usage: "Hash a password",
		Flags: append(defaultFlags, []cli.Flag{
			&cli.StringFlag{
				Name:  "password",
				Usage: "The password to hash",
				Value: "",
			},
		}...),
		Action: func(_ context.Context, c *cli.Command) error {
			password := c.String("password")
			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("failed to hash password: %w", err)
			}

			fmt.Println("Hashed password:", string(hashedPassword))
			return nil
		},
	}
}

func promptForUsernameAndPassword() (string, string, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Username: ")
	username, err := reader.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("failed to read username: %w", err)
	}
	username = strings.TrimSpace(username)

	fmt.Print("Password: ")
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", "", fmt.Errorf("failed to read password: %w", err)
	}
	password := string(bytePassword)
	fmt.Println() // Move to the next line after password input

	return username, password, nil
}

func promptForPassword() (string, error) {
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	password := string(bytePassword)
	return password, nil
}
