package cmd

import (
	"backend/database"
	"backend/server"
	"context"
	"fmt"
	"github.com/urfave/cli/v3"
	"log"
	"strings"
)

func ServerCli() *cli.Command {
	log.Println("Hello from server cli")
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
			&cli.IntFlag{
				Sources: cli.EnvVars("PORT"),
				Name:    "p2pport",
				Aliases: []string{"pp2p"},
				Value:   1985,
				Usage:   "server port",
			},
			&cli.StringSliceFlag{
				Sources: cli.EnvVars("PEERS"),
				Name:    "peers",
				Aliases: []string{"bp"},
				Usage:   "peers to connect to",
			},
			&cli.StringFlag{
				Sources: cli.EnvVars("ROOT_CREDENTIALS"),
				Name:    "root-credentials",
				Aliases: []string{"rc"},
				Usage:   "root credentials",
				Value:   "admin@mail.de:password",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			server.ServerStatus = "starting"
			server.Config = c // TODO: do cooler, more go-like way saw something something 'config *func(c options)'

			database.DB = database.SetupDatabase(c.String("db-backend"), c.String("db-path"), c.Bool("debug"))

			if c.Bool("debug") {
				database.SetupTestUsers()
			}

			s, fullHost := server.BackendServer(c.String("host"), c.Int("port"), c.Bool("debug"), c.Bool("ssl"))
			fmt.Printf("Starting server on %s\n", fullHost)
			fmt.Printf("Find API reference at %s/reference\n", fullHost)

			fmt.Println("Peers to connect to: ", c.StringSlice("peers"))
			// peers := c.StringSlice("peers")

			// Create default admin user
			rootCredentials := strings.Split(c.String("root-credentials"), ":")
			username := rootCredentials[0]
			password := rootCredentials[1]
			server.CreateRootUser(username, password)

			// start channels to other nodes
			// server.StartP2PFederation(int(c.Int("p2pport")), true, true, peers)
			server.CreateFederationHost(int(c.Int("p2pport")))
			server.ServerStatus = "running"
			err := s.ListenAndServe()

			if err != nil {
				return err
			}

			return nil
		},
	}

	return cmd
}
