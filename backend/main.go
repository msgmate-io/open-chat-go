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

	// use client cli when the second os.Args is "client"
	if len(os.Args) > 1 && os.Args[1] == "client" {
		if len(os.Args) == 2 {
			fmt.Println("client command requires a subcommand")
			return
		}
		runCmd = cmd.GetClientCmd(os.Args[2])
		if runCmd == nil {
			fmt.Println("invalid client command")
			return
		}
	} else if len(os.Args) > 1 && os.Args[1] == "install" {
		runCmd = cmd.InstallCli()
	} else if len(os.Args) > 1 && os.Args[1] == "ssh" {
		// Set up signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Start SSH server
		server, err := federation.NewSSHServer(2222, "Test123!")
		if err != nil {
			log.Fatal(err)
		}

		// Start server in goroutine
		errChan := make(chan error, 1)
		go func() {
			errChan <- server.Start()
		}()

		// Wait for either error or signal
		select {
		case err := <-errChan:
			if err != nil {
				log.Fatal(err)
			}
		case <-sigChan:
			log.Println("Shutting down SSH server...")
			server.Shutdown()
		}
		return
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
