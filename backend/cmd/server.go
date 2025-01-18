package cmd

import (
	"backend/api/federation"
	"backend/api/msgmate"
	"backend/database"
	"backend/server"
	"backend/server/util"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/urfave/cli/v3"
	"log"
	"os"
	"strings"
	"time"
)

func GetServerFlags() []cli.Flag {
	return []cli.Flag{
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
		&cli.BoolFlag{
			Sources: cli.EnvVars("SETUP_TEST_USERS"),
			Name:    "setup-test-users",
			Aliases: []string{"stu"},
			Value:   false,
			Usage:   "setup test users",
		},
		&cli.BoolFlag{
			Sources: cli.EnvVars("RESET_DB"),
			Name:    "reset-db",
			Aliases: []string{"rdb"},
			Value:   false,
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
			Sources: cli.EnvVars("P2PORT"),
			Name:    "p2pport",
			Aliases: []string{"pp2p"},
			Value:   0,
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
		&cli.StringFlag{
			Sources: cli.EnvVars("DEFAULT_NETOWORK_CREDENTIALS"),
			Name:    "default-network-credentials",
			Aliases: []string{"dnc"},
			// If empty default network is disabled
			Value: "",
			Usage: "default network credentials",
		},
		&cli.StringSliceFlag{
			Sources: cli.EnvVars("NET_BOOTSTRAP_PEERS"),
			Name:    "network-bootstrap-peers",
			Aliases: []string{"bs"},
			Value:   []string{},
			Usage:   "List of bootstrap peers to connect to on startup",
		},
		&cli.BoolFlag{
			Sources: cli.EnvVars("START_BOT"),
			Name:    "start-bot",
			Aliases: []string{"sb"},
			Value:   true,
			Usage:   "If the in-build msgmate bot should be started",
		},
	}
}

func ServerCli() *cli.Command {
	log.Println("Hello from server cli")
	cmd := &cli.Command{
		Name:  "boom",
		Usage: "make an explosive entrance",
		Flags: GetServerFlags(),
		Action: func(_ context.Context, c *cli.Command) error {
			server.ServerStatus = "starting"
			DB := database.SetupDatabase(database.DBConfig{
				Backend:  c.String("db-backend"),
				FilePath: c.String("db-path"),
				Debug:    c.Bool("debug"),
				ResetDB:  c.Bool("reset-db"),
			})

			if c.Bool("setup-test-users") {
				database.SetupTestUsers(DB)
			}

			// start channels to other nodes
			_, federationHandler, err := server.CreateFederationHost(DB, c.String("host"), int(c.Int("p2pport")), int(c.Int("port")))

			if err != nil {
				return err
			}

			s, ch, fullHost := server.BackendServer(DB, federationHandler, c.String("host"), c.Int("port"), c.Bool("debug"), c.Bool("ssl"), c.String("frontend-proxy"))
			fmt.Printf("Starting server on %s\n", fullHost)
			fmt.Printf("Find API reference at %s/reference\n", fullHost)

			rootCredentials := strings.Split(c.String("root-credentials"), ":")
			username := rootCredentials[0]
			password := rootCredentials[1]
			var adminUser *database.User

			if strings.HasPrefix(password, "hashed_") {
				hashedPassword := strings.TrimPrefix(password, "hashed_")
				// instead of providing the plain text password one may also provide a pre-hashed password
				err, adminUser = util.CreateUserPwPreHashed(DB, username, hashedPassword, true)
			} else {
				err, adminUser = util.CreateRootUser(DB, username, password)
			}

			if err != nil {
				return err
			}

			// Create the default msgmate-io bot
			botCredentials := strings.Split(c.String("default-bot"), ":")
			usernameBot := botCredentials[0]
			passwordBot := botCredentials[1]

			err, botUser := util.CreateUser(DB, usernameBot, passwordBot, false)
			if err != nil {
				return err
			}
			DB.Save(&botUser)

			err = msgmate.CreateOrUpdateBotProfile(DB, *botUser)
			if err != nil {
				return err
			}

			var usernameNetwork string
			var passwordNetwork string
			if c.String("default-network-credentials") != "" {
				// create default network
				networkCredentials := strings.Split(c.String("default-network-credentials"), ":")
				usernameNetwork = networkCredentials[0]
				passwordNetwork = networkCredentials[1]
				// call network.Create
				err = federationHandler.NetworkCreateRAW(DB, usernameNetwork, passwordNetwork)
				if err != nil {
					return err
				}
			}
			// Create default connection with admin user
			err = server.SetupBaseConnections(DB, adminUser.ID, botUser.ID)
			if err != nil {
				return err
			}

			// we must assure that the own node is in the meers store
			ownPeerId := federationHandler.Host.ID().String()
			var ownNode database.Node
			ownIdentity := federationHandler.GetIdentity()
			DB.Where("peer_id = ?", ownPeerId).First(&ownNode)
			hostname, err := os.Hostname()
			if err != nil {
				fmt.Println("Error:", err)
			}
			if ownNode.ID == 0 {
				log.Println("Own node not found, creating it")

				now := time.Now()
				_, err = federation.RegisterNodeRaw(
					DB,
					federationHandler,
					federation.RegisterNode{
						Name:         hostname,
						Addresses:    ownIdentity.ConnectMultiadress,
						AddToNetwork: usernameNetwork,
						LastChanged:  &now,
					},
					&now,
				)
				if err != nil {
					log.Println("Error registering own node", err)
				}
			} else {
				log.Println("Own node already existed updating it!")
				// first delete all existing adresses
				DB.Where("node_id = ?", ownNode.ID).Delete(&database.NodeAddress{})
				// then add the new ones
				adresses := []database.NodeAddress{}
				for _, address := range ownIdentity.ConnectMultiadress {
					adresses = append(adresses, database.NodeAddress{
						Address: address,
						NodeID:  ownNode.ID,
					})
					DB.Create(&adresses)
				}
				ownNode.NodeName = hostname
				ownNode.Addresses = adresses
				ownNode.LastChanged = time.Now()
				ownNode.PeerID = ownPeerId
				DB.Save(&ownNode)
			}
			server.InitializeNetworks(DB, federationHandler, c.String("host"), int(c.Int("port")))
			// Now we also have to register the bootstrap peers!
			for _, peer := range c.StringSlice("network-bootstrap-peers") {
				log.Println("Registering bootstrap peer", peer)
				decoded, err := base64.StdEncoding.DecodeString(peer)
				if err != nil {
					return fmt.Errorf("failed to decode bootstrap peer b64: %w", err)
				}
				var nodeInfo federation.NodeInfo
				err = json.Unmarshal(decoded, &nodeInfo)
				if err != nil {
					return fmt.Errorf("failed to unmarshal bootstrap peer: %w", err)
				}

				begginningOfTime := time.Time{}
				var registerNode federation.RegisterNode
				registerNode.Name = nodeInfo.Name
				registerNode.Addresses = nodeInfo.Addresses
				registerNode.AddToNetwork = usernameNetwork
				registerNode.LastChanged = &begginningOfTime
				_, err = federation.RegisterNodeRaw(
					DB,
					federationHandler,
					registerNode,
					&begginningOfTime,
				)
				if err != nil {
					log.Println("Error registering bootstrap peer", err)
				}
			}

			if c.Bool("start-bot") {
				go func() {
					time.Sleep(1 * time.Second)
					log.Printf("Starting bot ...")
					err := msgmate.StartBot(fullHost, ch, usernameBot, passwordBot)
					if err != nil {
						log.Printf("Error starting bot: %v", err)
					}
				}()
			}

			err = server.PreloadPeerstore(DB, federationHandler)
			if err != nil {
				return err
			}

			server.ServerStatus = "running"
			server.StartProxies(DB, federationHandler)

			err = s.ListenAndServe()

			if err != nil {
				return err
			}

			return nil
		},
	}

	return cmd
}
