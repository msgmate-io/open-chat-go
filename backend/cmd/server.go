package cmd

import (
	"backend/server"
	"context"
	"fmt"
	"github.com/urfave/cli/v3"
)

func ServerCli() *cli.Command {
	var port int64
	var host string
	var debug bool
	var ssl bool

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
			&cli.BoolFlag{
				Sources:     cli.EnvVars("DEBUG"),
				Name:        "debug",
				Destination: &debug,
				Aliases:     []string{"d"},
				Value:       false,
				Usage:       "enable debug mode",
			},
			&cli.StringFlag{
				Sources:     cli.EnvVars("HOST"),
				Name:        "host",
				Destination: &host,
				Aliases:     []string{"b"},
				Value:       "127.0.0.1",
				Usage:       "server bind address",
			},
			&cli.BoolFlag{
				Sources:     cli.EnvVars("SSL"),
				Name:        "ssl",
				Destination: &ssl,
				Aliases:     []string{"s"},
				Value:       false,
				Usage:       "enable ssl",
			},
			&cli.IntFlag{
				Sources:     cli.EnvVars("PORT"),
				Name:        "port",
				Destination: &port,
				Aliases:     []string{"p"},
				Value:       1984,
				Usage:       "server port",
			},
		},
		Action: func(context.Context, *cli.Command) error {

			s, fullHost := server.BackendServer(host, port, debug, ssl)
			fmt.Printf("Starting server on %s\n", fullHost)

			s.ListenAndServe()

			return nil
		},
	}

	return cmd
}
