package main

import (
	"backend/cmd"
	"context"
	"log"
	"os"
)

func main() {

	cmd := cmd.ServerCli()
	err := cmd.Run(context.Background(), os.Args)

	if err != nil {
		log.Fatal(err)
	}
}
