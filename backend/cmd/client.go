package cmd

import (
	"backend/client"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"os"
	"strings"
	"syscall"
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
	if action == "login" {
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
				// Use proper host precedence: CLI flag > environment variable > default
				host := getHostWithPrecedence(c)
				ocClient := client.NewClient(host)

				var username string
				var password string
				var err error
				username = c.String("username")
				password = c.String("password")

				if username == "" && password == "" {
					// request input of both username & password
					username, password, err = promptForUsernameAndPassword()
					if err != nil {
						return fmt.Errorf("failed to get username and password: %w", err)
					}
				} else if username != "" && password == "" {
					fmt.Println("Using username: ", username, " please enter password")
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
				// Write session ID to a file that can be sourced by the shell
				shell := os.Getenv("SHELL")
				if shell == "" {
					shell = "/bin/sh"
				}

				env := os.Environ()
				env = append(env, fmt.Sprintf("OPEN_CHAT_SESSION_ID=%s", sessionId))
				env = append(env, fmt.Sprintf("OPEN_CHAT_HOST=%s", host))
				env = append(env, fmt.Sprintf("OPEN_CHAT_USERNAME=%s", c.String("username")))
				env = append(env, fmt.Sprintf("OPEN_CHAT_SEAL_KEY=%s", password))

				fmt.Printf("Starting new shell with OPEN_CHAT_SESSION_ID set\n")
				proc, err := os.StartProcess(shell, []string{shell}, &os.ProcAttr{
					Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
					Env:   env,
				})

				_, err = proc.Wait()
				if err != nil {
					return fmt.Errorf("shell exited with error: %w", err)
				}
				return nil
			},
		}
	} else if action == "chats" {
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
					Name:  "offset",
					Usage: "The offset of the chats to return",
					Value: 0,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("List all chats")
				fmt.Println("Host: ", c.String("host"))
				fmt.Println("Session ID: ", c.String("session-id"))
				host := getHostWithPrecedence(c)
				fmt.Printf("DEBUG: Final host value: %s\n", host)
				ocClient := client.NewClient(host)
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
	} else if action == "metrics" {
		return &cli.Command{
			Name:  "metrics",
			Usage: "Get metrics",
			Flags: defaultFlags,
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Get metrics")
				ocClient := client.NewClient(c.String("host"))
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
	} else if action == "hash-password" {
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
	} else {
		return nil
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
