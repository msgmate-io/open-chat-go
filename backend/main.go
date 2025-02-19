package main

import (
	"backend/cmd"
	"context"
	"fmt"
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
//	@tag.name						integrations
//	@tag.description				These apis implement custom logic that allow open-chat to interface with seveal existing service or tools. Any functionality is specific to integrations, but they share a common api interface.
//
//	@tag.name						users
//	@tag.description				Everything user management related, users are also used to abstract access permissions. Chats have users as participants, only users share each others contact may create a shared chat.
//
//	@securityDefinitions.apikey		SessionAuth
//	@in								cookie
//	@name							session_id
//	@description					Session cookie obtained from login endpoint

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
		case "video":
			/**
			if len(os.Args) < 3 {
				fmt.Println("video command requires a video device")
				return
			}
			ws, err := federation.NewVideoServer(os.Args[2])
			if err != nil {
				log.Fatal(err)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/video", ws.ServeHTTP)

			fmt.Println("Starting video server at http://localhost:8080/video")
			if err := http.ListenAndServe(":8080", mux); err != nil {
				log.Fatal(err)
			}
			*/
			return
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
