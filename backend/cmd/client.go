package cmd

import (
	"backend/api/federation"
	"backend/client"
	"backend/server/util"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
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

func SSHSession(host string, port string, username string, password string) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Connect to the SSH server
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", host, port), config)
	if err != nil {
		log.Printf("Failed to dial: %v", err)
		return
	}
	defer client.Close()

	// Create a new session
	session, err := client.NewSession()
	if err != nil {
		log.Printf("Failed to create session: %v", err)
		return
	}
	defer session.Close()

	// Set up standard input, output, and error
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// Set terminal into raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Printf("Failed to set terminal to raw mode: %v", err)
		return
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Start an interactive shell
	err = session.Shell()
	if err != nil {
		log.Printf("Failed to start shell: %v", err)
		return
	}

	// Wait for the session to complete
	err = session.Wait()
	if err != nil {
		log.Printf("Session ended with error: %v", err)
	}
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
				ocClient := client.NewClient(c.String("host"))

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
				env = append(env, fmt.Sprintf("OPEN_CHAT_HOST=%s", c.String("host")))
				env = append(env, fmt.Sprintf("OPEN_CHAT_USERNAME=%s", c.String("username")))

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
				&cli.BoolFlag{
					Name:  "ls",
					Usage: "Pretty list all nodes",
					Value: false,
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

				if c.Bool("ls") {
					// Print headers
					fmt.Printf("%-50s %-50s %-25s\n", "peer_id", "node_name", "latest_contact")
					fmt.Println(strings.Repeat("-", 125))

					// Print each node's information
					for _, node := range nodes.Rows {
						fmt.Printf("%-50s %-50s %-25s\n",
							node.PeerID,
							node.NodeName,
							node.LatestContact)
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

				// Prepare a slice to store the results
				var results []struct {
					PeerID           string
					NodeName         string
					NodeVersion      string
					TotalCPUUsage    float64
					TotalMemoryUsage float64
				}

				// 3 - get metrics for each node
				for _, node := range nodes.Rows {
					fmt.Println("Getting metrics for node:", node.PeerID)

					if node.PeerID == ownNode.ID {
						fmt.Println("Skipping own node:", node.PeerID)
						err, metrics := ocClient.GetMetrics()
						if err != nil {
							fmt.Println("Failed to get metrics:", err)
							continue
						}
						results = append(results, struct {
							PeerID           string
							NodeName         string
							NodeVersion      string
							TotalCPUUsage    float64
							TotalMemoryUsage float64
						}{
							PeerID:           node.PeerID,
							NodeName:         node.NodeName,
							NodeVersion:      metrics.NodeVersion,
							TotalCPUUsage:    metrics.CPUInfo.Usage["all"],
							TotalMemoryUsage: metrics.MemoryInfo.UsedPercent,
						})
						continue
					} else if !util.Contains(keyNames, fmt.Sprintf("user_%s", node.PeerID)) {
						fmt.Println("No access credentials for node:", node.PeerID)
					} else {
						fmt.Println("Found access credentials for node:", node.PeerID)
						err, key := ocClient.RetrieveKey(fmt.Sprintf("user_%s", node.PeerID))
						if err != nil {
							fmt.Println("Failed to retrieve key:", err)
							continue
						}
						// fmt.Println("Key:", key, "using it to fetch metrics")
						// read key content as string
						keyContent := string(key.KeyContent)
						splitKeyContent := strings.Split(keyContent, ":")
						username := splitKeyContent[0]
						password := splitKeyContent[1]
						// get session id on that node
						err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, node.PeerID)
						if err != nil {
							fmt.Println("Failed to request session on remote node:", err)
							continue
						}
						// fmt.Println("Session ID:", sessionId)

						// now use send request to peer ID to
						err, resp := ocClient.RequestNodeByPeerId(node.PeerID, federation.RequestNode{
							Method: "GET",
							Path:   "/api/v1/metrics",
							Headers: map[string]string{
								"Cookie": fmt.Sprintf("session_id=%s", sessionId),
							},
						})
						if err != nil {
							fmt.Println("Failed to request node by peer id:", err)
							continue
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

						// Extract the required information
						nodeVersion := data["node_version"].(string)
						cpuInfo := data["cpu_info"].(map[string]interface{})
						usage := cpuInfo["usage"].(map[string]interface{})
						totalCPUUsage := usage["all"].(float64)

						memoryInfo := data["memory_info"].(map[string]interface{})
						totalMemoryUsage := memoryInfo["used_percent"].(float64)

						// Append the result
						results = append(results, struct {
							PeerID           string
							NodeName         string
							NodeVersion      string
							TotalCPUUsage    float64
							TotalMemoryUsage float64
						}{
							PeerID:           node.PeerID,
							NodeName:         node.NodeName,
							NodeVersion:      nodeVersion,
							TotalCPUUsage:    totalCPUUsage,
							TotalMemoryUsage: totalMemoryUsage,
						})
					}
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
				//fmt.Println("Identity:", identity, "Own Peer ID:", ownPeerId)

				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}
				randomPassword := ocClient.RandomPassword()

				randomSSHPort := ocClient.RandomSSHPort()
				// with the remote session id we can now setup the proies required!
				// Trough the proxy on the remote peer we need to setup:
				// 1 - ./backend client proxy --direction egress --target "server_username:SomeRandomPassword" --port 2222 --kind ssh
				// 2 - ./backend client proxy --direction ingress --origin "<local_peer_id>:2222" --port 2222
				// on the local peer using the existing client session we need to setup and ssh ingoing port:
				// ./backend client proxy --direction egress --target "<remote_peer_id>:2222" --port 2222
				// 3 - ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p 2222 server_username@localhost
				// ... when (3) is killed all proxies should be deleted!
				body := new(bytes.Buffer)
				proxyData := federation.CreateAndStartProxyRequest{
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
				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, federation.RequestNode{
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
				proxyData = federation.CreateAndStartProxyRequest{
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
				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, federation.RequestNode{
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

				fmt.Println("Random password:", randomPassword)
				fmt.Println("Random SSH port:", randomSSHPort)
				// wait few seconds for the proxies to be created
				fmt.Println("Connecting to ssh server...")
				time.Sleep(1 * time.Second)

				SSHSession("localhost", randomSSHPort, "tim", randomPassword)

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

				username, password, err := promptForUsernameAndPassword()
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}
				fmt.Println("Session ID:", sessionId)
				// now we can setup a simple 2 way proxy to upload the binary
				err, identity := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get identity: %w", err)
				}
				ownPeerId := identity.ID
				fmt.Println("Identity:", identity, "Own Peer ID:", ownPeerId)

				body := new(bytes.Buffer)
				json.NewEncoder(body).Encode(federation.RequestSelfUpdate{
					BinaryOwnerPeerId: ownPeerId,
					NetworkName:       c.String("network"),
				})

				err, _ = ocClient.RequestNodeByPeerId(remotePeerId, federation.RequestNode{
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
				fmt.Println("Update a node")
				ocClient := client.NewClient(c.String("host"))
				ocClient.SetSessionId(c.String("session-id"))

				// First we open a tcl tunnel to the node
				remotePeerId := c.String("node")

				username, password, err := promptForUsernameAndPassword()
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
				err, sessionId := ocClient.RequestSessionOnRemoteNode(username, password, remotePeerId)
				if err != nil {
					return fmt.Errorf("failed to request session on remote node: %w", err)
				}
				fmt.Println("Session ID:", sessionId)
				// now we can setup a simple 2 way proxy to upload the binary
				err, identity := ocClient.GetFederationIdentity()
				if err != nil {
					return fmt.Errorf("failed to get identity: %w", err)
				}
				ownPeerId := identity.ID
				fmt.Println("Identity:", identity, "Own Peer ID:", ownPeerId)

				err, resp := ocClient.RequestNodeByPeerId(remotePeerId, federation.RequestNode{
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
				err, resp := ocClient.RequestNodeByPeerId(remotePeerId, federation.RequestNode{
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
