package cmd

import (
	"backend/api"
	"context"
	"fmt"
	"github.com/urfave/cli/v3"
	"net/http"
)

func ServerCli() *cli.Command {
	var port int64
	var host string

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
				Sources:     cli.EnvVars("HOST"),
				Name:        "host",
				Destination: &host,
				Aliases:     []string{"b"},
				Value:       "0.0.0.0",
				Usage:       "server bind address",
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
			router := api.BackendRouting()

			fmt.Println(fmt.Sprintf("Starting Server at %s:%d", host, port))

			server := http.Server{
				Addr:    fmt.Sprintf("%s:%d", host, port),
				Handler: router,
			}

			server.ListenAndServe()

			return nil
		},
	}

	return cmd
}
