package cmd

import (
	"backend/queue"

	"github.com/urfave/cli/v3"
)

func GetRedisFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Sources: cli.EnvVars("REDIS_MODE"),
			Name:    "redis-mode",
			Usage:   "Redis mode: auto|external|embedded",
			Value:   queue.RedisModeAuto,
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("REDIS_URL"),
			Name:    "redis-url",
			Usage:   "Redis URL, e.g. redis://redis:6379/0",
			Value:   "redis://127.0.0.1:6379/0",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("REDIS_ADDR"),
			Name:    "redis-addr",
			Usage:   "Redis address if redis-url is not set",
			Value:   "127.0.0.1:6379",
		},
		&cli.StringFlag{
			Sources: cli.EnvVars("REDIS_PASSWORD"),
			Name:    "redis-password",
			Usage:   "Redis password if redis-url is not set",
			Value:   "",
		},
		&cli.IntFlag{
			Sources: cli.EnvVars("REDIS_DB"),
			Name:    "redis-db",
			Usage:   "Redis DB index if redis-url is not set",
			Value:   0,
		},
	}
}

func resolveRedisRuntime(c *cli.Command) (*queue.RedisRuntime, error) {
	return queue.ResolveRedisRuntime(
		c.String("redis-mode"),
		c.String("redis-url"),
		c.String("redis-addr"),
		c.String("redis-password"),
		int(c.Int("redis-db")),
	)
}
