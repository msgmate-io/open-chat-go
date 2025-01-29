package main

import (
	"backend/cmd"
	"bytes"
	"context"
	"fmt"
	ufcli "github.com/urfave/cli/v3"
	"log"
	"os"
	"os/exec"
)

// make version a variable so the build system can inject it
var version = "unknown"

func main() {
	var runCmd *ufcli.Command

	fmt.Println("FULL COMMAND: ", os.Args)
	if os.Args[0] == "/usr/local/bin/backend_updated" {
		fmt.Println("Detected update cycle, performing self-update...")
		// means we are in an update circle,
		// Copy the binary to /usr/local/bin/backend
		os.Rename("/usr/local/bin/backend_updated", "/usr/local/bin/backend")
		// Edit the service file to point to /usr/local/bin/backend
		serviceFilePath := "/etc/systemd/system/open-chat.service"
		serviceFile, err := os.ReadFile(serviceFilePath)
		if err != nil {
			log.Fatal(err)
		}
		serviceFile = bytes.ReplaceAll(serviceFile, []byte("ExecStart=/usr/local/bin/backend_updated"), []byte("ExecStart=/usr/local/bin/backend"))
		err = os.WriteFile(serviceFilePath, serviceFile, 0644)
		if err != nil {
			log.Fatal(err)
		}

		// Reload the systemd service
		exec.Command("systemctl", "daemon-reload").Run()
		exec.Command("systemctl", "restart", "open-chat").Run()
		return
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "client":
			if len(os.Args) == 2 {
				fmt.Println("client command requires a subcommand")
				return
			}
			runCmd = cmd.GetClientCmd(os.Args[2])
			if runCmd == nil {
				fmt.Println("invalid client command")
				return
			}
		case "install":
			runCmd = cmd.InstallCli()
		case "uninstall":
			runCmd = cmd.UninstallCli()
		default:
			runCmd = cmd.ServerCli()
		}
	} else {
		runCmd = cmd.ServerCli()
	}

	if runCmd != nil {
		err := runCmd.Run(context.Background(), os.Args)
		if err != nil {
			log.Fatal(err)
		}
	}
}
