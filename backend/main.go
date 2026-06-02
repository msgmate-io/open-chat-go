package main

import (
	"backend/cmd"
	"context"
	"log"
	"os"

	ufcli "github.com/urfave/cli/v3"
)

//	@title							Open Chat API
//	@version						1.0
//	@description					API for Open Chat application
//
//	@tag.name						chats
//	@tag.description				Chats hold a collection of messages and files or meta-data, they are central to how open-chat works and are used to hold information for interactions and integratins
//
// 	@tag.name						messages
//	@tag.description				Messages are the atomic data point of open-chat, they may hold any sort of supported information, they may also reference information in external locations. Messages are collected in a chat. Messages can have only one creator/sender but are received by all chat members.
//
//	@tag.name						users
//	@tag.description				Everything user management related, users are also used to abstract access permissions. Chats have users as participants, only users share each others contact may create a shared chat.
//
//	@securityDefinitions.apikey		SessionAuth
//	@in								cookie
//	@name							session_id
//	@description					Session cookie obtained from login endpoint

func main() {
	if len(os.Args) == 1 {
		os.Args = append(os.Args, "--help")
	}

	rootCmd := &ufcli.Command{
		Name:  "open-chat",
		Usage: "Open Chat command line interface",
		Commands: []*ufcli.Command{
			cmd.ServerCli(),
			cmd.WorkerCli(),
		},
	}

	if err := rootCmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
