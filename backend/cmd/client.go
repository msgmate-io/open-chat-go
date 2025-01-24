package cmd

import (
	"backend/api/federation"
	"backend/client"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/urfave/cli/v3"
	"os"
)

var defaultFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "host",
		Usage:   "The host to connect to",
		Value:   "http://localhost:1984",
		Sources: cli.EnvVars("OPEN_CHAT_HOST"),
	},
	&cli.StringFlag{
		Name:    "session-id",
		Usage:   "The session id to use",
		Value:   "",
		Sources: cli.EnvVars("OPEN_CHAT_SESSION_ID"),
	},
}

func GetClientCmd(action string) *cli.Command {
	if action == "login" {
		return &cli.Command{
			Name:  "login",
			Usage: "Login to the client",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "username",
					Usage: "The username to use",
					Value: "admin",
				},
				&cli.StringFlag{
					Name:  "password",
					Usage: "The password to use",
					Value: "password",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Login to the client")
				ocClient := client.NewClient(c.String("host"))
				err, sessionId := ocClient.LoginUser(c.String("username"), c.String("password"))
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
				env = append(env, fmt.Sprintf("OPEN_CHAT_HOST=%s", c.String("host")))

				fmt.Printf("Starting new shell with OPEN_CHAT_SESSION_ID set\n")
				proc, err := os.StartProcess(shell, []string{shell}, &os.ProcAttr{
					Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
					Env:   env,
				})

				fmt.Printf("Session ID: %s\n", sessionId)

				_, err = proc.Wait()
				if err != nil {
					return fmt.Errorf("shell exited with error: %w", err)
				}
				return nil
			},
		}
	} else if action == "proxy" {
		return &cli.Command{
			Name:  "proxy",
			Usage: "Proxy traffic to a node",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "direction",
					Usage: "The direction of the proxy",
					Value: "egress",
				},
				&cli.StringFlag{
					Name:  "origin",
					Usage: "The origin of the proxy",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "target",
					Usage: "The target of the proxy",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "port",
					Usage: "The port of the proxy",
					Value: "1984",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Proxy traffic to a node")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err := ocClient.CreateProxy(c.String("direction"), c.String("origin"), c.String("target"), c.String("port"))
				if err != nil {
					return fmt.Errorf("failed to proxy traffic: %w", err)
				}
				fmt.Println("Proxy created")
				return nil
			},
		}
	} else if action == "identity" || action == "id" {
		return &cli.Command{
			Name:  "identity",
			Usage: "Get the identity of the current node",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.BoolFlag{
					Name:    "base64",
					Aliases: []string{"b64"},
					Usage:   "Base64 encode the identity",
					Value:   false,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				err, identity := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get identity: %w", err)
				}
				if c.Bool("base64") {
					// we have to tranfor this into a federation.RegisterNode
					registerNode := federation.NodeInfo{
						Name:      identity.ID,
						Addresses: identity.ConnectMultiadress,
					}
					identityBytes, err := json.Marshal(registerNode)
					if err != nil {
						return fmt.Errorf("failed to marshal identity: %w", err)
					}
					base64Identity := base64.StdEncoding.EncodeToString(identityBytes)
					fmt.Println(base64Identity)
				} else {
					prettyIdentity, err := json.MarshalIndent(identity, "", "  ")
					if err != nil {
						return fmt.Errorf("failed to marshal identity: %w", err)
					}
					fmt.Println(string(prettyIdentity))
				}
				return nil
			},
		}
	} else if action == "register_node" || action == "register" {
		return &cli.Command{
			Name:  "register",
			Usage: "Register a node",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "name",
					Usage: "The name of the node",
					Value: "node",
				},
				&cli.StringSliceFlag{
					Name:  "addresses",
					Usage: "The addresses of the node",
					Value: []string{},
				},
				&cli.StringFlag{
					Name:  "b64",
					Usage: "Base64 encoded node registration data",
					Value: "",
				},
				&cli.BoolFlag{
					Name:  "request",
					Usage: "Request registration from the server",
					Value: false,
				},
				&cli.StringFlag{
					Name:  "network",
					Usage: "The network to register the node in",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Register a node")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				if c.String("b64") != "" {
					decoded, err := base64.StdEncoding.DecodeString(c.String("b64"))
					if err != nil {
						return fmt.Errorf("failed to decode b64: %w", err)
					}
					var nodeInfo federation.NodeInfo

					err = json.Unmarshal(decoded, &nodeInfo)
					if err != nil {
						return fmt.Errorf("failed to unmarshal node: %w", err)
					}

					var registerNode federation.RegisterNode
					registerNode.Name = nodeInfo.Name
					registerNode.Addresses = nodeInfo.Addresses
					if c.String("network") != "" {
						registerNode.AddToNetwork = c.String("network")
					}
					if c.Bool("request") {
						registerNode.RequestRegistration = true
					}
					err, node := ocClient.RegisterNode(registerNode.Name, registerNode.Addresses, registerNode.RequestRegistration, registerNode.AddToNetwork)
					if err != nil {
						return fmt.Errorf("failed to register node: %w", err)
					}

					prettyNode, err := json.MarshalIndent(node, "", "  ")
					if err != nil {
						return fmt.Errorf("failed to marshal node: %w", err)
					}
					fmt.Println(string(prettyNode))
				} else {
					fmt.Println("No b64 data provided, skipping registration")
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
				ocClient := client.NewClient(c.String("host"))
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
	} else if action == "whitelisted" {
		return &cli.Command{
			Name:  "whitelisted",
			Usage: "List all whitelisted peers",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "peer-id",
					Usage: "The peer id to check",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("List all whitelisted peers")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err, peers := ocClient.GetWhitelistedPeers()
				if err != nil {
					return fmt.Errorf("failed to get whitelisted peers: %w", err)
				}
				fmt.Println(peers)
				return nil
			},
		}
	} else if action == "tls" {
		return &cli.Command{
			Name:  "tls",
			Usage: "Solve an ACME challenge",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "hostname",
					Usage: "The hostname to solve the ACME challenge for",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "key-prefix",
					Usage: "The prefix for the keys to solve the ACME challenge for",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Solve an ACME challenge")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err, _ := ocClient.SolveACMEChallenge(c.String("hostname"), c.String("key-prefix"))
				if err != nil {
					return fmt.Errorf("failed to solve ACME challenge: %w", err)
				}
				fmt.Println("ACME challenge solved")
				return nil
			},
		}
	} else if action == "keys" {
		return &cli.Command{
			Name:  "keys",
			Usage: "List all keys",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.IntFlag{
					Name:  "page",
					Usage: "The page number to return",
					Value: 1,
				},
				&cli.IntFlag{
					Name:  "limit",
					Usage: "The number of keys to return",
					Value: 10,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("List all keys")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err, keys := ocClient.GetKeys(c.Int("page"), c.Int("limit"))
				if err != nil {
					return fmt.Errorf("failed to get keys: %w", err)
				}
				prettyKeys, err := json.MarshalIndent(keys, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal keys: %w", err)
				}
				fmt.Println(string(prettyKeys))
				return nil
			},
		}
	} else if action == "nodes" {
		return &cli.Command{
			Name:  "nodes",
			Usage: "List all nodes",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.IntFlag{
					Name:  "page",
					Usage: "The page number to return",
					Value: 1,
				},
				&cli.IntFlag{
					Name:  "limit",
					Usage: "The number of nodes to return",
					Value: 10,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("List all nodes")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err, nodes := ocClient.GetNodes(c.Int("page"), c.Int("limit"))
				if err != nil {
					return fmt.Errorf("failed to get nodes: %w", err)
				}
				prettyNodes, err := json.MarshalIndent(nodes, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal nodes: %w", err)
				}
				fmt.Println(string(prettyNodes))
				return nil
			},
		}
	} else {
		return nil
	}
}
