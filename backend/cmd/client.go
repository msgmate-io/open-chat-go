package cmd

import (
	"backend/api"
	"backend/client"
	"backend/database"
	"backend/server/util"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
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
				&cli.StringFlag{
					Name:  "network",
					Usage: "The network to create the proxy in",
					Value: "network",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Proxy traffic to a node")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err := ocClient.CreateProxy(c.String("direction"), c.String("origin"), c.String("target"), c.String("port"), c.String("network"))
				if err != nil {
					return fmt.Errorf("failed to proxy traffic: %w", err)
				}
				fmt.Println("Proxy created")
				return nil
			},
		}
	} else if action == "domain-proxy" {
		return &cli.Command{
			Name:  "domain-proxy",
			Usage: "Create a domain-based proxy with separate TLS certificate",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:     "domain",
					Usage:    "The domain name (e.g., proxy.example.com)",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "cert-prefix",
					Usage:    "The certificate prefix for TLS keys (e.g., proxy_example_com)",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "backend-port",
					Usage:    "The backend port to proxy to (e.g., 8080)",
					Required: true,
				},
				&cli.BoolFlag{
					Name:  "use-tls",
					Usage: "Enable TLS for the domain proxy",
					Value: true,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Printf("Creating domain proxy for %s\n", c.String("domain"))
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err := ocClient.CreateDomainProxy(
					c.String("domain"),
					c.String("cert-prefix"),
					c.String("backend-port"),
					c.Bool("use-tls"),
				)
				if err != nil {
					return fmt.Errorf("failed to create domain proxy: %w", err)
				}
				fmt.Println("Domain proxy created successfully")
				return nil
			},
		}
	} else if action == "delete-proxy" {
		return &cli.Command{
			Name:  "delete-proxy",
			Usage: "Delete a proxy by UUID",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:     "uuid",
					Usage:    "The proxy UUID to delete",
					Required: true,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				proxyUUID := c.String("uuid")
				fmt.Printf("Deleting proxy %s\n", proxyUUID)
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err := ocClient.DeleteProxy(proxyUUID)
				if err != nil {
					return fmt.Errorf("failed to delete proxy: %w", err)
				}
				fmt.Println("Proxy deleted successfully")
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
					// we have to tranfor this into a api.RegisterNode
					registerNode := api.NodeInfo{
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
					var nodeInfo api.NodeInfo

					err = json.Unmarshal(decoded, &nodeInfo)
					if err != nil {
						return fmt.Errorf("failed to unmarshal node: %w", err)
					}

					var registerNode api.RegisterNode
					registerNode.Name = nodeInfo.Name
					registerNode.Addresses = nodeInfo.Addresses
					if c.String("network") != "" {
						registerNode.AddToNetwork = c.String("network")
					}
					err, node := ocClient.RegisterNode(registerNode.Name, registerNode.Addresses, registerNode.AddToNetwork)
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
	} else if action == "renew-tls" {
		return &cli.Command{
			Name:  "renew-tls",
			Usage: "Renew an existing SSL certificate",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "hostname",
					Usage: "The hostname to renew the certificate for",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "key-prefix",
					Usage: "The prefix for the keys to renew the certificate for",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Renewing SSL certificate")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err, _ := ocClient.RenewTLSCertificate(c.String("hostname"), c.String("key-prefix"))
				if err != nil {
					return fmt.Errorf("failed to renew TLS certificate: %w", err)
				}
				fmt.Println("TLS certificate renewed successfully")
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
				&cli.BoolFlag{
					Name:  "ls",
					Usage: "Pretty list all nodes",
					Value: false,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("List all nodes")
				fmt.Printf("DEBUG: Host from CLI: %s\n", c.String("host"))
				fmt.Printf("DEBUG: OPEN_CHAT_HOST env var: %s\n", os.Getenv("OPEN_CHAT_HOST"))
				host := getHostWithPrecedence(c)
				fmt.Printf("DEBUG: Final host value: %s\n", host)
				ocClient := client.NewClient(host)
				ocClient.SetSessionId(c.String("session-id"))
				err, ownIdentity := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get own identity: %w", err)
				}
				err, nodes := ocClient.GetNodes(c.Int("page"), c.Int("limit"))
				if err != nil {
					return fmt.Errorf("failed to get nodes: %w", err)
				}

				if c.Bool("ls") {
					// Print headers
					fmt.Printf("%-50s %-50s %-25s\n", "peer_id", "node_name", "latest_contact")
					fmt.Println(strings.Repeat("-", 125))

					// Print each node's information
					for _, node := range nodes.Rows {
						latestContactTime, err := time.Parse(time.RFC3339, node.LatestContact.Format(time.RFC3339))
						if err != nil {
							return fmt.Errorf("failed to parse latest contact time: %w", err)
						}

						status := "(offline)"
						if time.Since(latestContactTime) <= 3*time.Minute {
							status = "(online)"
						}

						if node.PeerID == ownIdentity.ID {
							status = "(self)"
						}

						fmt.Printf("%-50s %-50s %-25s\n",
							node.PeerID,
							node.NodeName,
							fmt.Sprintf("%s %s", status, node.LatestContact))
					}
				} else {
					prettyNodes, err := json.MarshalIndent(nodes, "", "  ")
					if err != nil {
						return fmt.Errorf("failed to marshal nodes: %w", err)
					}
					fmt.Println(string(prettyNodes))
				}
				return nil
			},
		}
	} else if action == "all-metrics" {
		return &cli.Command{
			Name:  "all-metrics",
			Usage: "Get all metrics",
			Flags: defaultFlags,
			Action: func(_ context.Context, c *cli.Command) error {
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				// 0 - get own node info
				err, ownNode := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get own node: %w", err)
				}
				fmt.Println("Own node:", ownNode)

				// 1 - get all nodes
				err, nodes := ocClient.GetNodes(1, 1000)
				if err != nil {
					return fmt.Errorf("failed to get nodes: %w", err)
				}

				// 2 - get all key names to see if access creds for all nodes are present
				err, keyNames := ocClient.GetKeyNames()
				if err != nil {
					return fmt.Errorf("failed to get key names: %w", err)
				}
				// fmt.Println("Key names:", keyNames)

				// Helper struct to store node access info
				type nodeAccess struct {
					node     database.Node
					username string
					password string
				}

				// Helper struct for collecting results
				type metricsResult struct {
					node    database.Node
					metrics map[string]interface{}
					err     error
				}

				// First determine which nodes we can access
				var accessibleNodes []nodeAccess
				fmt.Println("Checking node accessibility...")

				// Check own node first
				if ownNode != nil {
					accessibleNodes = append(accessibleNodes, nodeAccess{
						node: database.Node{
							PeerID:   ownNode.ID,
							NodeName: "self",
						},
					})
				}

				// Check other nodes
				for _, node := range nodes.Rows {
					if node.PeerID == ownNode.ID {
						continue // Skip own node as it's already added
					}

					if !util.Contains(keyNames, fmt.Sprintf("user_%s", node.PeerID)) {
						fmt.Printf("No access credentials for node: %s\n", node.PeerID)
						continue
					}

					err, key := ocClient.RetrieveKey(fmt.Sprintf("user_%s", node.PeerID))
					if err != nil {
						fmt.Printf("Failed to retrieve key for node %s: %v\n", node.PeerID, err)
						continue
					}

					keyContent := string(key.KeyContent)
					splitKeyContent := strings.Split(keyContent, ":")
					if len(splitKeyContent) != 2 {
						fmt.Printf("Invalid key content format for node %s\n", node.PeerID)
						continue
					}

					accessibleNodes = append(accessibleNodes, nodeAccess{
						node: database.Node{
							PeerID:   node.PeerID,
							NodeName: node.NodeName,
						},
						username: splitKeyContent[0],
						password: splitKeyContent[1],
					})
				}

				fmt.Printf("Found %d accessible nodes\n", len(accessibleNodes))

				// Create a channel for results
				resultsChan := make(chan metricsResult, len(accessibleNodes))

				// Create a semaphore to limit concurrent requests
				sem := make(chan struct{}, 5) // Limit to 5 concurrent requests

				// Launch goroutines to fetch metrics
				var wg sync.WaitGroup
				for _, access := range accessibleNodes {
					wg.Add(1)
					go func(access nodeAccess) {
						defer wg.Done()

						// Acquire semaphore
						sem <- struct{}{}
						defer func() { <-sem }()

						if access.node.PeerID == ownNode.ID {
							// Handle own node
							err, metrics := ocClient.GetMetrics()
							if err != nil {
								resultsChan <- metricsResult{node: access.node, err: err}
								return
							}

							// Convert metrics to map[string]interface{} format
							metricsBytes, err := json.Marshal(metrics)
							if err != nil {
								resultsChan <- metricsResult{node: access.node, err: err}
								return
							}

							var metricsMap map[string]interface{}
							if err := json.Unmarshal(metricsBytes, &metricsMap); err != nil {
								resultsChan <- metricsResult{node: access.node, err: err}
								return
							}

							resultsChan <- metricsResult{node: access.node, metrics: metricsMap}
							return
						}

						// Handle remote node
						err, sessionId := ocClient.RequestSessionOnRemoteNode(access.username, access.password, access.node.PeerID)
						if err != nil {
							resultsChan <- metricsResult{node: access.node, err: fmt.Errorf("session request failed: %w", err)}
							return
						}

						err, resp := ocClient.RequestNodeByPeerId(access.node.PeerID, api.RequestNode{
							Method: "GET",
							Path:   "/api/v1/metrics",
							Headers: map[string]string{
								"Cookie": fmt.Sprintf("session_id=%s", sessionId),
							},
						})
						if err != nil {
							resultsChan <- metricsResult{node: access.node, err: fmt.Errorf("metrics request failed: %w", err)}
							return
						}
						defer resp.Body.Close()

						bodyBytes, err := io.ReadAll(resp.Body)
						if err != nil {
							resultsChan <- metricsResult{node: access.node, err: fmt.Errorf("reading response failed: %w", err)}
							return
						}

						var metricsMap map[string]interface{}
						if err := json.Unmarshal(bodyBytes, &metricsMap); err != nil {
							resultsChan <- metricsResult{node: access.node, err: fmt.Errorf("unmarshaling failed: %w", err)}
							return
						}

						resultsChan <- metricsResult{node: access.node, metrics: metricsMap}
					}(access)
				}

				// Wait for all goroutines to complete
				go func() {
					wg.Wait()
					close(resultsChan)
				}()

				// Collect results
				var results []struct {
					PeerID           string
					NodeName         string
					NodeVersion      string
					TotalCPUUsage    float64
					TotalMemoryUsage float64
				}

				for result := range resultsChan {
					if result.err != nil {
						fmt.Printf("Error fetching metrics for node %s: %v\n", result.node.PeerID, result.err)
						continue
					}

					// Extract metrics with error handling
					nodeVersion, _ := result.metrics["node_version"].(string)

					cpuInfo, _ := result.metrics["cpu_info"].(map[string]interface{})
					usage, _ := cpuInfo["usage"].(map[string]interface{})
					totalCPUUsage, _ := usage["all"].(float64)

					memoryInfo, _ := result.metrics["memory_info"].(map[string]interface{})
					totalMemoryUsage, _ := memoryInfo["used_percent"].(float64)

					results = append(results, struct {
						PeerID           string
						NodeName         string
						NodeVersion      string
						TotalCPUUsage    float64
						TotalMemoryUsage float64
					}{
						PeerID:           result.node.PeerID,
						NodeName:         result.node.NodeName,
						NodeVersion:      nodeVersion,
						TotalCPUUsage:    totalCPUUsage,
						TotalMemoryUsage: totalMemoryUsage,
					})
				}

				// Print the results in a table format
				fmt.Printf("%-70s %-15s %-20s %-25s\n", "peer_id (node_name)", "node_version", "total cpu usage percent", "total memory usage percent")
				fmt.Println(strings.Repeat("-", 130))
				for _, result := range results {
					fmt.Printf("%-70s %-15s %-20.2f %-25.2f\n", fmt.Sprintf("%s (%s)", result.PeerID, result.NodeName), result.NodeVersion, result.TotalCPUUsage, result.TotalMemoryUsage)
				}

				return nil
			},
		}
	} else if action == "key-names" {
		return &cli.Command{
			Name:  "key-names",
			Usage: "List all key names",
			Flags: defaultFlags,
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("List all keys")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err, keys := ocClient.GetKeyNames()
				if err != nil {
					return fmt.Errorf("failed to get keys: %w", err)
				}
				fmt.Println("Keys:")
				fmt.Println(keys)
				return nil
			},
		}
	} else if action == "create-key" {
		return &cli.Command{
			Name:  "create-key",
			Usage: "Create a key",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "key-name",
					Usage: "The name of the key",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "key-type",
					Usage: "The type of the key",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "key-content",
					Usage: "The content of the key",
					Value: "",
				},
				&cli.BoolFlag{
					Name:  "sealed",
					Usage: "Is the key encrypted?",
					Value: false,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Create a key")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err := ocClient.CreateKey(c.String("key-name"), c.String("key-type"), []byte(c.String("key-content")), c.Bool("sealed"))
				if err != nil {
					return fmt.Errorf("failed to create key: %w", err)
				}
				fmt.Println("Key created")
				return nil
			},
		}
	} else if action == "request-video" {
		return &cli.Command{
			Name:  "request-video",
			Usage: "Request video",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "node",
					Usage: "The node to request video from",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "network",
					Usage: "The network to request video from",
					Value: "network",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				// on the remote node:
				// backend client proxy --direction egress --port <random-video-port> --kind video
				// backend client proxy --direction ingress --origin "<requesting-peer-id>:<random-video-port>" --port <random-video-port> --kind tcp
				// on the local node:
				// backend client proxy --direction egress --type tcp --target "<remote-peer-id>:<random-video-port>" --port <random-video-port>
				// to view the stream visit: http://localhost:<local-port-2>

				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				networkName := c.String("network")
				username, password, err := retrieveOrPromptForCreds(ocClient, c.String("node"))
				if err != nil {
					return fmt.Errorf("failed to retrieve or prompt for credentials: %w", err)
				}
				// fmt.Println("Login string entered:", username, password)
				remotePeerId := c.String("node")

				err, identity := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get identity: %w", err)
				}
				ownPeerId := identity.ID

				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}
				randomServerPort := ocClient.RandomSSHPort()

				body := new(bytes.Buffer)
				json.NewEncoder(body).Encode(api.CreateAndStartProxyRequest{
					Direction:     "egress",
					TrafficOrigin: "/dev/video0:video",
					TrafficTarget: ":",
					Port:          randomServerPort,
					Kind:          "video",
					NetworkName:   networkName,
				})
				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method: "POST",
					Path:   "/api/v1/federation/nodes/proxy",
					Headers: map[string]string{
						"Cookie": fmt.Sprintf("session_id=%s", sessionId),
					},
					Body: string(body.Bytes()),
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}

				// creeate the remote ingress proxy
				body = new(bytes.Buffer)
				json.NewEncoder(body).Encode(api.CreateAndStartProxyRequest{
					Direction:     "ingress",
					TrafficOrigin: ownPeerId + ":" + randomServerPort,
					TrafficTarget: "",
					Port:          randomServerPort,
					Kind:          "tcp",
					NetworkName:   networkName,
				})
				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method: "POST",
					Path:   "/api/v1/federation/nodes/proxy",
					Headers: map[string]string{
						"Cookie": fmt.Sprintf("session_id=%s", sessionId),
					},
					Body: string(body.Bytes()),
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}

				// create the local egress proxy
				err = ocClient.CreateProxy("egress", "", remotePeerId+":"+randomServerPort, randomServerPort, networkName)
				if err != nil {
					return fmt.Errorf("failed to create proxy: %w", err)
				}

				fmt.Println("Proxies created, you can now view the video by visiting: http://localhost:" + randomServerPort)

				return nil
			},
		}
	} else if action == "shell" {
		return &cli.Command{
			Name:  "shell",
			Usage: "Open a shell to a node",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "node",
					Usage: "The node to open a shell to",
					Value: "",
				},
				&cli.StringFlag{
					Name:    "network",
					Usage:   "The network to open a shell to",
					Value:   "network",
					Sources: cli.EnvVars("OPEN_CHAT_DEFAULT_NETWORK"),
				},
				&cli.BoolFlag{
					Name:    "no-connect",
					Usage:   "Do not connect to the ssh server, just create the proxies",
					Aliases: []string{"nc"},
					Value:   false,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Attempting to establish ssh connection to node", c.String("node"))
				// prompt the user to enter the 'nodes' admin password!

				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				networkName := c.String("network")
				username, password, err := retrieveOrPromptForCreds(ocClient, c.String("node"))
				if err != nil {
					return fmt.Errorf("failed to retrieve or prompt for credentials: %w", err)
				}
				// fmt.Println("Login string entered:", username, password)
				remotePeerId := c.String("node")

				err, identity := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get identity: %w", err)
				}
				ownPeerId := identity.ID

				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}
				randomPassword := ocClient.RandomPassword()

				randomSSHPort := ocClient.RandomSSHPort()
				// with the remote session id we can now setup the proxies required!
				// Trough the proxy on the remote peer we need to setup:
				// 1 - ./backend client proxy --direction egress --target "server_username:SomeRandomPassword" --port 2222 --kind ssh
				// 2 - ./backend client proxy --direction ingress --origin "<local_peer_id>:2222" --port 2222
				// on the local peer using the existing client session we need to setup and ssh ingoing port:
				// ./backend client proxy --direction egress --target "<remote_peer_id>:2222" --port 2222
				// 3 - ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p 2222 server_username@localhost
				// ... when (3) is killed all proxies should be deleted!

				// On local machiene:
				// 1 - Start some server on port 2222
				// 2 - ingress local peer  --target "<local-peer-id>:2222" --port 2222 --origin "<remote-peer-id>:2222"
				// on remote peer
				// 3 - egress node port 2222 --origin "<remote-peer-id>:2222" --target "<local-peer-id>:2222"
				// 4 - ssl proxy to port 2222
				// use_as_ngrok(
				// 	local_peer_id='',
				// 	local_port=2222,
				// 	remote_peer_id='',
				//  remote_host='example.timsproxy.msgmate.io',
				//  remote_key_prefix='ssltimsproxy'
				// )
				// UI: 'port forward to remote node'
				// - dropdown 'network'
				// - dropdown 'remote peer'
				// - input 'port'
				// - checkbox 'use SSL on remote node'
				//   - host-name
				//   - key-prefix on remote

				body := new(bytes.Buffer)
				proxyData := api.CreateAndStartProxyRequest{
					UseTLS:        false,
					KeyPrefix:     "",
					NodeUUID:      "",
					Port:          randomSSHPort,
					Kind:          "ssh",
					Direction:     "egress",
					TrafficOrigin: "ssh:" + randomPassword,
					TrafficTarget: "tim:" + randomPassword,
					NetworkName:   networkName,
				}
				json.NewEncoder(body).Encode(proxyData)
				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method: "POST",
					Path:   "/api/v1/federation/nodes/proxy",
					Headers: map[string]string{
						"Cookie": fmt.Sprintf("session_id=%s", sessionId),
					},
					Body: string(body.Bytes()),
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}
				// setup the ingoing proxy on the local peer
				body = new(bytes.Buffer)
				proxyData = api.CreateAndStartProxyRequest{
					UseTLS:        false,
					KeyPrefix:     "",
					NodeUUID:      "",
					Port:          randomSSHPort,
					Kind:          "ssh",
					Direction:     "ingress",
					TrafficOrigin: ownPeerId + ":" + randomSSHPort,
					TrafficTarget: "",
					NetworkName:   networkName,
				}
				json.NewEncoder(body).Encode(proxyData)
				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method: "POST",
					Path:   "/api/v1/federation/nodes/proxy",
					Headers: map[string]string{
						"Cookie": fmt.Sprintf("session_id=%s", sessionId),
					},
					Body: string(body.Bytes()),
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}

				// now finally we can register the proxy on the local node!
				err = ocClient.CreateProxy("egress", "", remotePeerId+":"+randomSSHPort, randomSSHPort, networkName)
				if err != nil {
					return fmt.Errorf("failed to create proxy: %w", err)
				}

				if !c.Bool("no-connect") {
					// Provide both SSH connection options
					fmt.Println("Connecting to ssh server...")
					time.Sleep(1 * time.Second)

					// Provide web terminal URL
					webTerminalURL := fmt.Sprintf("http://%s/federation/terminal?port=%s&password=%s",
						c.String("host"), randomSSHPort, randomPassword)
					fmt.Println("Web Terminal URL:", webTerminalURL)

					// Connect via traditional SSH
					port, _ := strconv.Atoi(randomSSHPort)
					api.SSHSession("localhost", port, "tim", randomPassword)
				} else {
					fmt.Println("Proxies created, but not connecting to ssh server")
					fmt.Println("You can connect to the ssh server by running:")
					fmt.Println("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p", randomSSHPort, "tim@"+"localhost")
					fmt.Println("Using the password:", randomPassword)

					// Provide web terminal URL
					webTerminalURL := fmt.Sprintf("http://%s/terminal.html?port=%s&password=%s",
						c.String("host"), randomSSHPort, randomPassword)
					fmt.Println("Or access via web terminal:", webTerminalURL)
				}

				return nil
			},
		}
	} else if action == "update-node" {
		return &cli.Command{
			Name:  "update_node",
			Usage: "Update a node",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "node",
					Usage: "The node to update",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "network",
					Usage: "The network to update the node on",
					Value: "network",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Update a node")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				// First we open a tcl tunnel to the node
				remotePeerId := c.String("node")

				username, password, err := retrieveOrPromptForCreds(ocClient, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}
				// now we can setup a simple 2 way proxy to upload the binary
				err, identity := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get identity: %w", err)
				}
				ownPeerId := identity.ID

				body := new(bytes.Buffer)
				json.NewEncoder(body).Encode(api.RequestSelfUpdate{
					BinaryOwnerPeerId: ownPeerId,
					NetworkName:       c.String("network"),
				})

				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method: "POST",
					Path:   "/api/v1/bin/request-self-update",
					Headers: map[string]string{
						"Cookie": fmt.Sprintf("session_id=%s", sessionId),
					},
					Body: string(body.Bytes()),
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}

				return nil
			},
		}
	} else if action == "proxies" {
		return &cli.Command{
			Name:  "proxies",
			Usage: "List proxies",
			Flags: defaultFlags,
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("List proxies")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err, proxies := ocClient.ListProxies(1, 10)
				if err != nil {
					return fmt.Errorf("failed to list proxies: %w", err)
				}
				prettyProxies, err := json.MarshalIndent(proxies, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal proxies: %w", err)
				}
				fmt.Println(string(prettyProxies))
				return nil
			},
		}
	} else if action == "get-proxies" {
		return &cli.Command{
			Name:  "get-proxies",
			Usage: "Get proxies",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "node",
					Usage: "The node to get proxies from",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Get proxies")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				// ret remote session id
				remotePeerId := c.String("node")
				username, password, err := retrieveOrPromptForCreds(ocClient, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}

				// now request the proxies endpoing of the remote node
				err, resp := ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method: "GET",
					Path:   "/api/v1/federation/proxies/list",
					Headers: map[string]string{
						"Cookie": fmt.Sprintf("session_id=%s", sessionId),
					},
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}
				defer resp.Body.Close()

				bodyBytes, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to read response body: %w", err)
				}
				var data map[string]interface{}
				err = json.Unmarshal(bodyBytes, &data)
				if err != nil {
					return fmt.Errorf("failed to unmarshal response body: %w", err)
				}
				prettyData, err := json.MarshalIndent(data, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal data: %w", err)
				}
				fmt.Println("Proxies:", string(prettyData))
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
	} else if action == "get-metrics" {
		return &cli.Command{
			Name:  "get-metrics",
			Usage: "Get metrics",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "node",
					Usage: "The node to get metrics from",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Fetching metrics for node", c.String("node"))
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				// First we open a tcl tunnel to the node
				remotePeerId := c.String("node")
				username, password, err := retrieveOrPromptForCreds(ocClient, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to retrieve or prompt for credentials: %w", err)
				}
				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}

				err, resp := ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method: "GET",
					Path:   "/api/v1/metrics",
					Headers: map[string]string{
						"Cookie": fmt.Sprintf("session_id=%s", sessionId),
					},
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}

				defer resp.Body.Close()

				bodyBytes, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to read response body: %w", err)
				}

				var data map[string]interface{}
				err = json.Unmarshal(bodyBytes, &data)
				if err != nil {
					return fmt.Errorf("failed to unmarshal response body: %w", err)
				}

				prettyData, err := json.MarshalIndent(data, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal data: %w", err)
				}
				fmt.Println("Metrics:", string(prettyData))

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
	} else if action == "delete-key" {
		return &cli.Command{
			Name:  "delete-key",
			Usage: "Delete a key",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "key-name",
					Usage: "The key to delete",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Delete key")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				err := ocClient.DeleteKey(c.String("key-name"))
				if err != nil {
					return fmt.Errorf("failed to delete key: %w", err)
				}
				return nil
			},
		}
	} else if action == "download-binary" {
		return &cli.Command{
			Name:  "download-binary",
			Usage: "Download a binary",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:  "node",
					Usage: "The node to download the binary from",
					Value: "",
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				// downloads the binary of a federated node
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))
				remotePeerId := c.String("node")
				err, resp := ocClient.RequestNodeByPeerId(remotePeerId, api.RequestNode{
					Method:  "GET",
					Path:    "/api/v1/bin/download",
					Headers: map[string]string{},
					Body:    "",
				})
				if err != nil {
					return fmt.Errorf("failed to request node by peer id: %w", err)
				}
				defer resp.Body.Close()

				// Check the status code
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
				}

				// Log content length
				fmt.Printf("Content-Length: %s\n", resp.Header.Get("Content-Length"))

				// take the binary from the response and write it to a file
				binary, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to read response body: %w", err)
				}
				err = os.WriteFile("binary_downloaded", binary, 0644)
				if err != nil {
					return fmt.Errorf("failed to write binary to file: %w", err)
				}
				fmt.Println("Binary downloaded and saved to binary.tar.gz")

				return nil
			},
		}
	} else if action == "edit-service" {
		return &cli.Command{
			Name:  "edit-service",
			Usage: "Edit the OpenChat systemd service file",
			Flags: defaultFlags,
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Opening the OpenChat systemd service file in vim...")

				// Check if the service file exists
				serviceFile := "/etc/systemd/system/open-chat.service"
				_, err := os.Stat(serviceFile)
				if os.IsNotExist(err) {
					return fmt.Errorf("service file %s does not exist", serviceFile)
				}

				// Execute sudo vim to edit the file
				cmd := exec.Command("sudo", "vim", serviceFile)
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				err = cmd.Run()
				if err != nil {
					return fmt.Errorf("failed to edit service file: %w", err)
				}

				fmt.Println("Service file edited. Reloading systemd daemon...")

				// Reload systemd daemon
				reloadCmd := exec.Command("sudo", "systemctl", "daemon-reload")
				err = reloadCmd.Run()
				if err != nil {
					return fmt.Errorf("failed to reload systemd daemon: %w", err)
				}

				fmt.Println("Restarting open-chat service...")

				// Restart the open-chat service
				restartCmd := exec.Command("sudo", "systemctl", "restart", "open-chat")
				err = restartCmd.Run()
				if err != nil {
					return fmt.Errorf("failed to restart open-chat service: %w", err)
				}

				fmt.Println("Service restarted successfully!")
				return nil
			},
		}
	} else if action == "logs" {
		return &cli.Command{
			Name:  "logs",
			Usage: "Show and follow logs for the OpenChat service",
			Flags: defaultFlags,
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Following logs for open-chat service. Press Ctrl+C to exit.")

				// Run journalctl with -f flag to follow logs
				cmd := exec.Command("journalctl", "-u", "open-chat.service", "-f")
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				return cmd.Run()
			},
		}
	} else if action == "install-signal" {
		return &cli.Command{
			Name:  "install-signal",
			Usage: "Install Signal REST API integration",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:     "alias",
					Usage:    "Alias for the Signal integration",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "phone-number",
					Usage:    "Phone number for Signal",
					Required: true,
				},
				&cli.IntFlag{
					Name:     "port",
					Usage:    "Port for the Signal REST API",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "mode",
					Usage:    "Mode for Signal CLI (normal or json-rpc)",
					Value:    "normal",
					Required: false,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Installing Signal REST API integration...")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				err := ocClient.InstallSignalIntegration(
					c.String("alias"),
					c.String("phone-number"),
					int(c.Int("port")),
					c.String("mode"),
				)

				if err != nil {
					return fmt.Errorf("failed to install Signal integration: %w", err)
				}

				fmt.Println("Signal REST API integration installed successfully")
				return nil
			},
		}
	} else if action == "uninstall-signal" {
		return &cli.Command{
			Name:  "uninstall-signal",
			Usage: "Uninstall Signal REST API integration",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:     "alias",
					Usage:    "Alias for the Signal integration to uninstall",
					Required: true,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Uninstalling Signal REST API integration...")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				err := ocClient.UninstallSignalIntegration(c.String("alias"))
				if err != nil {
					return fmt.Errorf("failed to uninstall Signal integration: %w", err)
				}

				fmt.Println("Signal REST API integration uninstalled successfully")
				return nil
			},
		}
	} else if action == "signal-whitelist-add" {
		return &cli.Command{
			Name:  "signal-whitelist-add",
			Usage: "Add a phone number to the Signal whitelist",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:     "alias",
					Usage:    "Alias for the Signal integration",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "phone-number",
					Usage:    "Phone number to add to the whitelist",
					Required: true,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Adding phone number to Signal whitelist...")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				err := ocClient.AddToSignalWhitelist(
					c.String("alias"),
					c.String("phone-number"),
				)

				if err != nil {
					return fmt.Errorf("failed to add phone number to Signal whitelist: %w", err)
				}

				fmt.Printf("Successfully added %s to whitelist for Signal integration '%s'\n",
					c.String("phone-number"), c.String("alias"))
				return nil
			},
		}
	} else if action == "signal-whitelist-remove" {
		return &cli.Command{
			Name:  "signal-whitelist-remove",
			Usage: "Remove a phone number from the Signal whitelist",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:     "alias",
					Usage:    "Alias for the Signal integration",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "phone-number",
					Usage:    "Phone number to remove from the whitelist",
					Required: true,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Removing phone number from Signal whitelist...")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				err := ocClient.RemoveFromSignalWhitelist(
					c.String("alias"),
					c.String("phone-number"),
				)

				if err != nil {
					return fmt.Errorf("failed to remove phone number from Signal whitelist: %w", err)
				}

				fmt.Printf("Successfully removed %s from whitelist for Signal integration '%s'\n",
					c.String("phone-number"), c.String("alias"))
				return nil
			},
		}
	} else if action == "signal-whitelist-list" {
		return &cli.Command{
			Name:  "signal-whitelist-list",
			Usage: "List all phone numbers in the Signal whitelist",
			Flags: append(defaultFlags, []cli.Flag{
				&cli.StringFlag{
					Name:     "alias",
					Usage:    "Alias for the Signal integration",
					Required: true,
				},
			}...),
			Action: func(_ context.Context, c *cli.Command) error {
				fmt.Println("Retrieving Signal whitelist...")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				err, whitelist := ocClient.GetSignalWhitelist(c.String("alias"))
				if err != nil {
					return fmt.Errorf("failed to get Signal whitelist: %w", err)
				}

				fmt.Printf("Whitelist for Signal integration '%s':\n", c.String("alias"))
				if len(whitelist) == 0 {
					fmt.Println("  (empty)")
				} else {
					for _, number := range whitelist {
						fmt.Printf("  - %s\n", number)
					}
				}
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

func retrieveOrPromptForCreds(ocClient *client.Client, peerId string) (string, string, error) {
	// first get all key names
	err, keyNames := ocClient.GetKeyNames()
	if err != nil {
		return "", "", fmt.Errorf("failed to get key names: %w", err)
	}

	// if the key names contain the own peer id, then we can use the key
	if util.Contains(keyNames, fmt.Sprintf("user_%s", peerId)) {
		// retrieve the key
		err, key := ocClient.RetrieveKey(fmt.Sprintf("user_%s", peerId))
		if err != nil {
			return "", "", fmt.Errorf("failed to retrieve key: %w", err)
		}
		split := strings.Split(string(key.KeyContent), ":")
		return split[0], split[1], nil
	}

	// otherwise we prompt for the username and password
	username, password, err := promptForUsernameAndPassword()
	if err != nil {
		return "", "", fmt.Errorf("failed to prompt for username and password: %w", err)
	}
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
