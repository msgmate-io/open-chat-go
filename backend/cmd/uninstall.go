package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/urfave/cli/v3"
)

func UninstallCli() *cli.Command {
	return &cli.Command{
		Name:  "uninstall",
		Usage: "Uninstall backend service and remove all files",
		Action: func(_ context.Context, c *cli.Command) error {
			// Check if running as root
			if os.Geteuid() != 0 {
				return fmt.Errorf("this command must be run as root (use sudo)")
			}

			const (
				serviceName     = "open-chat"
				binaryName      = "backend"
				installPath     = "/usr/local/bin"
				servicePath     = "/etc/systemd/system"
				installedDBPath = "/var/lib/openchat"
			)

			// Stop and disable the service
			fmt.Println("Stopping and disabling service...")
			if exec.Command("systemctl", "is-active", "--quiet", serviceName).Run() == nil {
				if err := exec.Command("systemctl", "stop", serviceName).Run(); err != nil {
					return fmt.Errorf("failed to stop service: %w", err)
				}
			}
			if err := exec.Command("systemctl", "disable", serviceName).Run(); err != nil {
				return fmt.Errorf("failed to disable service: %w", err)
			}

			// Remove the service file
			serviceFilePath := filepath.Join(servicePath, serviceName+".service")
			fmt.Println("Removing service file...")
			if err := os.Remove(serviceFilePath); err != nil {
				return fmt.Errorf("failed to remove service file: %w", err)
			}

			// Reload systemd to recognize the removal
			if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
				return fmt.Errorf("failed to reload systemd: %w", err)
			}

			// Remove the binary
			binaryPath := filepath.Join(installPath, binaryName)
			fmt.Println("Removing binary...")
			if err := os.Remove(binaryPath); err != nil {
				return fmt.Errorf("failed to remove binary: %w", err)
			}

			// Remove the database and other files
			fmt.Println("Removing database and other files...")
			if err := os.RemoveAll(installedDBPath); err != nil {
				return fmt.Errorf("failed to remove database files: %w", err)
			}

			fmt.Println("Uninstallation complete!")
			return nil
		},
	}
}
