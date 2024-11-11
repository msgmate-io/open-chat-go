package cmd

import (
	"backend/database"
	"backend/server"
	"context"
	"fmt"
	"github.com/urfave/cli/v3"
)

func ServerCli() *cli.Command {
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
				Sources: cli.EnvVars("DB_PATH"),
				Name:    "db-path",
				Aliases: []string{"dp"},
				Value:   "data.db",
				Usage:   "For sqlite the path to the database file",
			},
			&cli.BoolFlag{
				Sources: cli.EnvVars("DEBUG"),
				Name:    "debug",
				Aliases: []string{"d"},
				Value:   true, // TODO default to false
				Usage:   "enable debug mode",
			},
			&cli.StringFlag{
				Sources: cli.EnvVars("HOST"),
				Name:    "host",
				Aliases: []string{"b"},
				Value:   "127.0.0.1",
				Usage:   "server bind address",
			},
			&cli.BoolFlag{
				Sources: cli.EnvVars("SSL"),
				Name:    "ssl",
				Aliases: []string{"s"},
				Value:   false,
				Usage:   "enable ssl",
			},
			&cli.IntFlag{
				Sources: cli.EnvVars("PORT"),
				Name:    "port",
				Aliases: []string{"p"},
				Value:   1984,
				Usage:   "server port",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {

			database.DB = database.SetupDatabase(c.String("db-backend"), c.String("db-path"), c.Bool("debug"))

			s, fullHost := server.BackendServer(c.String("host"), c.Int("port"), c.Bool("debug"), c.Bool("ssl"))
			fmt.Printf("Starting server on %s\n", fullHost)

			s.ListenAndServe()

			return nil
		},
	}

	return cmd
}
