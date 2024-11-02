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
				Sources: cli.EnvVars("DB_BACKEND"),
				Name:    "db-backend",
				Aliases: []string{"db"},
				Value:   "sqlite",
				Usage:   "database driver to use",
			},
			&cli.StringFlag{
				Sources: cli.EnvVars("HOST"),
				Name:    "bind",
				Aliases: []string{"h"},
				Value:   "0.0.0.0",
				Usage:   "server bind address",
			},
			&cli.IntFlag{
				Sources: cli.EnvVars("PORT"),
				Name:    "port",
				Aliases: []string{"p"},
				Value:   1984,
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
