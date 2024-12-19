package main

import (
	"backend/cmd"
	"context"
	"fmt"
	ufcli "github.com/urfave/cli/v3"
	"log"
	"os"
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
	} else {
		runCmd = cmd.ServerCli()
	}
	err := runCmd.Run(context.Background(), os.Args)

	if err != nil {
		log.Fatal(err)
	}
}
