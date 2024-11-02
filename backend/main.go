package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:  "boom",
		Usage: "make an explosive entrance",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Sources: cli.EnvVars("MVP_DB_BACKEND"),
				Name:    "db",
				Value:   "sqlite",
				Usage:   "database driver to use",
			},
			&cli.StringFlag{
				Sources: cli.EnvVars("MVP_DB_DSN"),
				Name:    "dbdns",
				Value:   "db=./database",
				Usage:   "database connect string",
			},
			&cli.StringFlag{
				Sources: cli.EnvVars("MVP_HOST"),
				Name:    "bind",
				Value:   "127.0.0.1",
				Usage:   "server bind address",
			},
			&cli.IntFlag{
				Sources: cli.EnvVars("MVP_PORT"),
				Name:    "port",
				Value:   4000,
				Usage:   "server port",
			},
		},
		Action: func(context.Context, *cli.Command) error {
			fmt.Println("boom! I say!")
			return nil
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
