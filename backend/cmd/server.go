package cmd

import (
	"backend/api"
	"context"
	"fmt"
	"github.com/rs/cors"
	"github.com/urfave/cli/v3"
	"net/http"
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
				Value:       "0.0.0.0",
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
			router := api.BackendRouting()
			var protocol string
			var fullHost string

			if ssl {
				protocol = "https"
			} else {
				protocol = "http"
			}

			fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

			fmt.Println(fmt.Sprintf("Starting Server at %s", fullHost))

			server := http.Server{
				Addr: fmt.Sprintf("%s:%d", host, port),
				Handler: api.CreateStack(
					api.Logging,
					cors.New(cors.Options{
						AllowedOrigins:   []string{fullHost},
						AllowCredentials: true,
						Debug:            debug,
					}).Handler,
				)(router),
			}

			server.ListenAndServe()

			return nil
		},
	}

	return cmd
}
