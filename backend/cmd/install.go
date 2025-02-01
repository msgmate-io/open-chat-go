package cmd

import (
	"backend/api/federation"
	"backend/database"
	"backend/server"
	"backend/server/util"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/urfave/cli/v3"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func InstallCli() *cli.Command {
	return &cli.Command{
		Name:  "install",
		Usage: "Install backend as a systemd service",
		Flags: GetServerFlags(),
		Action: func(_ context.Context, c *cli.Command) error {

			// Check if running as root
			if os.Geteuid() != 0 {
				return fmt.Errorf("this command must be run as root (use sudo)")
			}
			installedDBPath := "/var/lib/openchat"

			// check if service is already installed
			if _, err := os.Stat("/etc/systemd/system/open-chat.service"); err == nil {
				fmt.Println("service is already installed performing update instead!")
				// check if service is running
				if exec.Command("systemctl", "is-active", "--quiet", "open-chat").Run() == nil {
					fmt.Println("service is running, stopping it...")
					if err := exec.Command("systemctl", "stop", "open-chat").Run(); err != nil {
						return fmt.Errorf("failed to stop service: %w", err)
					}
				}
			} else {
				fmt.Println("service is not installed, installing for the first time...")
				DB := database.SetupDatabase(c.String("db-backend"), "./data.db", c.Bool("debug"), true)

				// - create default admin user
				rootCredentials := strings.Split(c.String("root-credentials"), ":")
				username := rootCredentials[0]
				password := rootCredentials[1]

				if strings.HasPrefix(password, "hashed_") {
					hashedPassword := strings.TrimPrefix(password, "hashed_")
					// instead of providing the plain text password one may also provide a pre-hashed password
					err, _ = util.CreateUserPwPreHashed(DB, username, hashedPassword, true)
				} else {
					err, _ = util.CreateRootUser(DB, username, password)
				}
				if err != nil {
					return fmt.Errorf("failed to create root user: %w", err)
				}

				// create default bot user
				botCredentials := strings.Split(c.String("default-bot"), ":")
				usernameBot := botCredentials[0]
				passwordBot := botCredentials[1]
				err, _ = util.CreateUser(DB, usernameBot, passwordBot, false)
				if err != nil {
					return fmt.Errorf("failed to create bot user: %w", err)
				}

				// create the default network

				_, federationHandler, err := server.CreateFederationHost(DB, c.String("host"), int(c.Int("p2pport")), int(c.Int("port")))

				if err != nil {
					return err
				}
				var usernameNetwork string
				var passwordNetwork string
				if c.String("default-network-credentials") != "" {
					// create default network
					networkCredentials := strings.Split(c.String("default-network-credentials"), ":")
					usernameNetwork = networkCredentials[0]
					passwordNetwork = networkCredentials[1]
					// call network.Create
					err = federationHandler.NetworkCreateRAW(DB, usernameNetwork, passwordNetwork)
					if err != nil {
						return err
					}
				}

				// noe we also setup bootstrap peers
				for _, peer := range c.StringSlice("network-bootstrap-peers") {
					log.Println("Registering bootstrap peer", peer)
					decoded, err := base64.StdEncoding.DecodeString(peer)
					if err != nil {
						return fmt.Errorf("failed to decode bootstrap peer b64: %w", err)
					}
					var nodeInfo federation.NodeInfo
					err = json.Unmarshal(decoded, &nodeInfo)
					if err != nil {
						return fmt.Errorf("failed to unmarshal bootstrap peer: %w", err)
					}

					begginningOfTime := time.Time{}
					var registerNode federation.RegisterNode
					registerNode.Name = nodeInfo.Name
					registerNode.Addresses = nodeInfo.Addresses
					registerNode.AddToNetwork = usernameNetwork
					registerNode.LastChanged = &begginningOfTime
					_, err = federation.RegisterNodeRaw(
						DB,
						federationHandler,
						registerNode,
						&begginningOfTime,
					)
					if err != nil {
						log.Println("Error registering bootstrap peer", err)
					}
				}

				// check if directory exists else create it
				if _, err := os.Stat(installedDBPath); os.IsNotExist(err) {
					os.MkdirAll(installedDBPath, 0755)
				}

				// Now move the initalized DB to that path
				os.Rename("./data.db", filepath.Join(installedDBPath, "data.db"))

			}

			const (
				serviceName = "open-chat"
				binaryName  = "backend"
				installPath = "/usr/local/bin"
				servicePath = "/etc/systemd/system"
			)

			// Check if service already exists
			serviceFilePath := filepath.Join(servicePath, serviceName+".service")
			serviceExists := false
			if _, err := os.Stat(serviceFilePath); err == nil {
				serviceExists = true
				fmt.Println("Existing service found, updating binary and restarting service...")
			}

			// Stop existing service if running
			if exec.Command("systemctl", "is-active", "--quiet", serviceName).Run() == nil {
				fmt.Println("Stopping existing service...")
				/*
					if err := exec.Command("systemctl", "stop", serviceName).Run(); err != nil {
						return fmt.Errorf("failed to stop service: %w", err)
					}*/
			}

			/** Kill all backend processes except the current one
			fmt.Println("Killing all other backend processes...")
			currentPID := os.Getpid()
			fmt.Println("Current PID:", currentPID)
			out, err := exec.Command("pgrep", "-a", "-f", binaryName).Output()
			if err != nil {
				return fmt.Errorf("failed to list backend processes: %w", err)
			}

			for _, line := range strings.Split(string(out), "\n") {
				if line == "" {
					continue
				}
				fields := strings.Fields(line)
				pidStr := fields[0]
				cmdLine := strings.Join(fields[1:], " ")

				pid, err := strconv.Atoi(pidStr)
				if err != nil {
					continue
				}

				// Check if the command line matches the current process
				if pid != currentPID && !strings.Contains(cmdLine, strconv.Itoa(currentPID)) {
					fmt.Println("Killing process:", pid)
					exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
				}
			}

			// Wait for a moment to ensure all processes are terminated
			time.Sleep(2 * time.Second)

			// Verify no other backend processes are running
			out, err = exec.Command("pgrep", "-f", binaryName).Output()
			if err != nil {
				return fmt.Errorf("failed to list backend processes: %w", err)
			}

			for _, pidStr := range strings.Fields(string(out)) {
				pid, err := strconv.Atoi(pidStr)
				if err != nil {
					continue
				}
				if pid != currentPID {
					fmt.Println("Still running process:", pid)
					return fmt.Errorf("other backend processes are still running, please try again")
				}
			} */

			// Copy binary to install path
			fmt.Printf("Installing binary to %s/%s...\n", installPath, binaryName)

			srcPath := os.Args[0]
			dstPath := filepath.Join(installPath, binaryName)

			src, err := os.Open(srcPath)
			if err != nil {
				return fmt.Errorf("failed to open source binary: %w", err)
			}
			defer src.Close()

			dst, err := os.OpenFile(dstPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("failed to create destination binary: %w", err)
			}
			defer dst.Close()

			if !serviceExists {
				// Create service file only if it doesn't exist
				fmt.Println("Creating systemd service...")
				serviceContent := fmt.Sprintf(`[Unit]
Description=Open Chat Backend Server Service
After=network.target

[Service]
Type=simple
Environment="DB_BACKEND=%s"
Environment="DB_PATH=%s" 
Environment="P2PORT=%s"
Environment="HOST=%s"
Environment="PORT=%s"
Environment="DEBUG=%t"
ExecStart=%s/%s
Restart=always
RestartSec=3
User=root

[Install]
WantedBy=multi-user.target
`, c.String("db-backend"),
					fmt.Sprintf("%s/data.db", installedDBPath),
					strconv.Itoa(int(c.Int("p2pport"))),
					c.String("host"),
					strconv.Itoa(int(c.Int("port"))),
					c.Bool("debug"),
					installPath,
					binaryName)

				if err := os.WriteFile(serviceFilePath, []byte(serviceContent), 0644); err != nil {
					return fmt.Errorf("failed to write service file: %w", err)
				}
			}

			if _, err := io.Copy(dst, src); err != nil {
				return fmt.Errorf("failed to copy binary: %w", err)
			}

			// Reload systemd daemon
			if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
				return fmt.Errorf("failed to reload systemd: %w", err)
			}

			// Enable and start service
			fmt.Println("Enabling and starting service...")
			if err := exec.Command("systemctl", "enable", serviceName).Run(); err != nil {
				return fmt.Errorf("failed to enable service: %w", err)
			}

			if err := exec.Command("systemctl", "start", serviceName).Run(); err != nil {
				return fmt.Errorf("failed to start service: %w", err)
			}

			fmt.Println("Installation complete! Service status:")
			cmd := exec.Command("systemctl", "status", serviceName)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		},
	}
}
