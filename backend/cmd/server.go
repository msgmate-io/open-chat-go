package cmd

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server"
	"context"
	"fmt"
	"github.com/urfave/cli/v3"
	"log"
	"strings"
	"time"
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
			&cli.StringFlag{
				Sources: cli.EnvVars("ROOT_CREDENTIALS"),
				Name:    "root-credentials",
				Aliases: []string{"rc"},
				Usage:   "root credentials",
				Value:   "admin:password",
			},
			&cli.StringFlag{
				Sources: cli.EnvVars("DEFAULT_BOT_CREDENTIALS"),
				Name:    "default-bot",
				Aliases: []string{"botc"},
				Usage:   "bot login credentials",
				Value:   "bot:password",
			},
			&cli.StringFlag{
				Sources: cli.EnvVars("FRONTEND_PROXY"),
				Name:    "frontend-proxy",
				Aliases: []string{"fpx"},
				Usage:   "Path '' for no proxy, e.g.: 'http://localhost:5173/' for remix",
				Value:   "",
			},
			&cli.BoolFlag{
				Sources: cli.EnvVars("START_BOT"),
				Name:    "start-bot",
				Aliases: []string{"sb"},
				Value:   true,
				Usage:   "If the in-build msgmate bot should be started",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			server.ServerStatus = "starting"
			DB := database.SetupDatabase(c.String("db-backend"), c.String("db-path"), c.Bool("debug"))

			if c.Bool("debug") {
				database.SetupTestUsers(DB)
			}

			// start channels to other nodes
			federationHost, err := server.CreateFederationHost(c.String("host"), int(c.Int("p2pport")), int(c.Int("port")))

			if err != nil {
				return err
			}

			s, fullHost := server.BackendServer(DB, federationHost, c.String("host"), c.Int("port"), c.Bool("debug"), c.Bool("ssl"), c.String("frontend-proxy"))
			fmt.Printf("Starting server on %s\n", fullHost)
			fmt.Printf("Find API reference at %s/reference\n", fullHost)

			rootCredentials := strings.Split(c.String("root-credentials"), ":")
			username := rootCredentials[0]
			password := rootCredentials[1]
			err, adminUser := server.CreateRootUser(DB, username, password)

			if err != nil {
				return err
			}

			// Create the default msgmate-io bot
			botCredentials := strings.Split(c.String("default-bot"), ":")
			usernameBot := botCredentials[0]
			passwordBot := botCredentials[1]

			err, botUser := server.CreateUser(DB, usernameBot, passwordBot, false)
			if err != nil {
				return err
			}

			// Create default connection with admin user
			err = server.SetupBaseConnections(DB, adminUser.ID, botUser.ID)
			if err != nil {
				return err
			}

			server.ServerStatus = "running"

			if c.Bool("start-bot") {
				go func() {
					time.Sleep(1 * time.Second)
					log.Printf("Starting bot ...")
					err := msgmate.StartBot(usernameBot, passwordBot)
					if err != nil {
						log.Printf("Error starting bot: %v", err)
					}
				}()
			}

			err = s.ListenAndServe()

			if err != nil {
				return err
			}

			return nil
		},
	}

	return cmd
}
