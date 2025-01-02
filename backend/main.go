package main

import (
	"backend/api/federation"
	"backend/cmd"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	ufcli "github.com/urfave/cli/v3"
)

// make version a variable so the build system can inject it
var version = "unknown"

func main() {
	var runCmd *ufcli.Command

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
