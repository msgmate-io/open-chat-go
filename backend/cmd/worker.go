package cmd

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/integrations"
	"backend/queue"
	"backend/workqueue"
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/urfave/cli/v3"

	"backend/logging"
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
			&cli.StringFlag{
				Sources: cli.EnvVars("OPEN_CHAT_HOST"),
				Name:    "backend-host",
				Usage:   "Backend base URL used by async bot tasks",
				Value:   workqueue.DefaultBackendHost,
			},
			&cli.BoolFlag{
				Sources: cli.EnvVars("DB_SKIP_MIGRATIONS"),
				Name:    "skip-migrations",
				Usage:   "skip the automatic database migration pass (set on secondary workers so they do not race the primary process)",
				Value:   false,
			},
			&cli.BoolFlag{
				Sources: cli.EnvVars("WORKER_SCHEDULER"),
				Name:    "run-scheduler",
				Usage:   "run the integration scheduler in this process (exactly one process must own it)",
				Value:   true,
			},
		}, GetRedisFlags()...),
		Action: func(ctx context.Context, c *cli.Command) error {
			integrations.EnsureLoaded()
			database.RegisterExternalModels(integrations.AdditionalModels()...)
			for _, migration := range integrations.AdditionalMigrations() {
				database.RegisterExternalMigrations(database.FunctionMigration{
					Name: migration.Name,
					Run:  migration.Run,
				})
			}

			redisRuntime, err := resolveRedisRuntime(c)
			if err != nil {
				return err
			}
			defer redisRuntime.Cleanup()
			if redisRuntime.Mode == queue.RedisModeEmbedded {
				if redisRuntime.FallbackReason != nil {
					logging.Errorf("External redis unavailable (%v); started embedded redis at %s", redisRuntime.FallbackReason, redisRuntime.Address)
				} else {
					logging.Infof("Started embedded redis at %s", redisRuntime.Address)
				}
			} else {
				logging.Infof("Using external redis at %s", redisRuntime.Address)
			}

			DB := database.SetupDatabase(database.DBConfig{
				Backend:        c.String("db-backend"),
				FilePath:       c.String("db-path"),
				Debug:          c.Bool("debug"),
				ResetDB:        false,
				SkipMigrations: c.Bool("skip-migrations"),
			})
			database.SetGlobalDB(DB)
			processor := &queue.Processor{
				DB:          DB,
				BackendHost: c.String("backend-host"),
			}

			server := asynq.NewServer(
				redisRuntime.ConnOpt,
				asynq.Config{
					Concurrency: int(c.Int("asynq-concurrency")),
					Queues: map[string]int{
						queue.QueueDefault: 1,
					},
					ErrorHandler: queue.BotReplyTerminalDrain(DB),
				},
			)

			queueClient := asynq.NewClient(redisRuntime.ConnOpt)
			defer queueClient.Close()
			queueInspector := asynq.NewInspector(redisRuntime.ConnOpt)
			// ci#724/ci#747: in-turn tools without request scope (coder_schedule)
			// and the self-resume runtime resolve the process-wide handles.
			workqueue.SetGlobalQueueHandles(queueClient, queueInspector)

			if c.Bool("run-scheduler") {
				integrationScheduler, err := queue.StartIntegrationSchedulers(redisRuntime.ConnOpt)
				if err != nil {
					return err
				}
				if integrationScheduler != nil {
					defer integrationScheduler.Shutdown()
				}
				// ci#744 (RC1, depends on ci#744): seed the declarative
				// self-message schedules (OPEN_CHAT_SELF_MESSAGE_SCHEDULES) in
				// the same single-owner gate, so a worker-only deployment also
				// re-fires a configured actor. No-op when nothing is configured.
				//
				// ci#785: self-messaging is an optional convenience on top of the
				// queue scheduler, so a deployment that does not know the
				// configured bot (an unseeded CI database, a renamed user, a bot
				// without a chat) must degrade to a loud log line and keep
				// working, exactly like the embedded server path in
				// backend/cmd/server.go. Never abort the worker for it.
				selfMessageRuntime, selfErr := msgmate.StartSelfMessageRuntime(ctx, DB, queueClient, queueInspector)
				switch {
				case selfErr != nil:
					logging.Errorf("self-message schedules disabled, idle interactions will not self-resume: %v", selfErr)
				case selfMessageRuntime != nil:
					defer selfMessageRuntime.Shutdown()
					logging.Infof("Armed the self-message schedules (ci#744); idle interactions now self-resume")
				default:
					logging.Infof("No self-message schedules configured (%s unset)", msgmate.SelfMessageScheduleEnvVar)

				}
			} else {
				logging.Infof("Integration scheduler disabled (run-scheduler=false); another process must own it")
			}

			logging.Infof("Starting asynq worker with concurrency=%d", c.Int("asynq-concurrency"))
			if err := server.Run(processor.NewServeMux()); err != nil {
				return fmt.Errorf("asynq worker failed: %w", err)
			}

			return nil
		},
	}
}
