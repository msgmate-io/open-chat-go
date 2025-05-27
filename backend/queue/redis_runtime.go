package queue

import (
	"fmt"
	"strings"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

const (
	RedisModeAuto     = "auto"
	RedisModeExternal = "external"
	RedisModeEmbedded = "embedded"
)

type RedisRuntime struct {
	ConnOpt        asynq.RedisConnOpt
	Mode           string
	Address        string
	FallbackReason error
	Cleanup        func()
}

func ResolveRedisRuntime(mode string, redisURL string, redisAddr string, redisPassword string, redisDB int) (*RedisRuntime, error) {
	normalizedMode, err := normalizeRedisMode(mode)
	if err != nil {
		return nil, err
	}

	if normalizedMode == RedisModeEmbedded {
		return resolveEmbeddedRedisRuntime()
	}

	connOpt, err := BuildRedisConnOpt(redisURL, redisAddr, redisPassword, redisDB)
	if err != nil {
		return nil, err
	}

	if normalizedMode == RedisModeExternal {
		return &RedisRuntime{
			ConnOpt: connOpt,
			Mode:    RedisModeExternal,
			Address: redisAddressFromConnOpt(connOpt),
			Cleanup: func() {},
		}, nil
	}

	if pingErr := pingRedis(connOpt); pingErr == nil {
		return &RedisRuntime{
			ConnOpt: connOpt,
			Mode:    RedisModeExternal,
			Address: redisAddressFromConnOpt(connOpt),
			Cleanup: func() {},
		}, nil
	} else {
		runtime, embeddedErr := resolveEmbeddedRedisRuntime()
		if embeddedErr != nil {
			return nil, fmt.Errorf("redis auto mode failed: external unavailable (%v) and embedded startup failed: %w", pingErr, embeddedErr)
		}
		runtime.FallbackReason = pingErr
		return runtime, nil
	}
}

func normalizeRedisMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = RedisModeAuto
	}

	switch mode {
	case RedisModeAuto, RedisModeExternal, RedisModeEmbedded:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid redis mode %q, expected one of: auto, external, embedded", mode)
	}
}

func pingRedis(connOpt asynq.RedisConnOpt) error {
	client := asynq.NewClient(connOpt)
	defer client.Close()
	return client.Ping()
}

func resolveEmbeddedRedisRuntime() (*RedisRuntime, error) {
	redisServer, err := miniredis.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to start embedded redis: %w", err)
	}

	address := redisServer.Addr()
	return &RedisRuntime{
		ConnOpt: asynq.RedisClientOpt{Addr: address},
		Mode:    RedisModeEmbedded,
		Address: address,
		Cleanup: redisServer.Close,
	}, nil
}

func redisAddressFromConnOpt(connOpt asynq.RedisConnOpt) string {
	switch opt := connOpt.(type) {
	case asynq.RedisClientOpt:
		return opt.Addr
	case asynq.RedisFailoverClientOpt:
		return strings.Join(opt.SentinelAddrs, ",")
	default:
		return ""
	}
}
