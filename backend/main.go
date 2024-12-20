package main

import (
	"backend/cmd"
	"context"
	"log"
	"os"
)

// make version a variable so the build system can inject it
var version = "unknown"

func main() {

	if true {
		cmd := cmd.ServerCli()
		err := cmd.Run(context.Background(), os.Args)

		if err != nil {
			log.Fatal(err)
		}
	} else {
		// cmd.Start()
		cmd.Start()
	}
}
