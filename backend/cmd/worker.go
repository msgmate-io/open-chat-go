package cmd

import (
	"backend/database"
	"backend/queue"
	"context"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/urfave/cli/v3"
)

func WorkerCli() *cli.Command {
	return &cli.Command{
		Name:  "worker",
		Usage: "start the Open Chat background worker",
		Flags: append([]cli.Flag{
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
				Value:   true,
				Usage:   "enable debug mode",
			},
			&cli.IntFlag{
				Sources: cli.EnvVars("ASYNQ_CONCURRENCY"),
				Name:    "asynq-concurrency",
				Usage:   "number of concurrent worker goroutines",
				Value:   10,
			},
		}, GetRedisFlags()...),
		Action: func(_ context.Context, c *cli.Command) error {
			redisConnOpt, err := resolveRedisConnOpt(c)
			if err != nil {
				return err
			}

			DB := database.SetupDatabase(database.DBConfig{
				Backend:  c.String("db-backend"),
				FilePath: c.String("db-path"),
				Debug:    c.Bool("debug"),
				ResetDB:  false,
			})

			processor := &queue.Processor{DB: DB}

			server := asynq.NewServer(
				redisConnOpt,
				asynq.Config{
					Concurrency: int(c.Int("asynq-concurrency")),
					Queues: map[string]int{
						queue.QueueDefault: 1,
					},
				},
			)

			log.Printf("Starting asynq worker with concurrency=%d", c.Int("asynq-concurrency"))
			if err := server.Run(processor.NewServeMux()); err != nil {
				return fmt.Errorf("asynq worker failed: %w", err)
			}

			return nil
		},
	}
}
