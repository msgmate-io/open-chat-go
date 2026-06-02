package queue

import (
	"fmt"

	"github.com/hibiken/asynq"
)

func BuildRedisConnOpt(redisURL string, redisAddr string, redisPassword string, redisDB int) (asynq.RedisConnOpt, error) {
	if redisURL != "" {
		opt, err := asynq.ParseRedisURI(redisURL)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
		}
		return opt, nil
	}

	if redisAddr == "" {
		return nil, fmt.Errorf("redis address is required when REDIS_URL is not set")
	}

	return asynq.RedisClientOpt{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       redisDB,
	}, nil
}
