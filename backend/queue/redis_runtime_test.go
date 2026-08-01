package queue

import (
	"testing"

	"github.com/hibiken/asynq"
)

func TestResolveRedisRuntimeEmbedded(t *testing.T) {
	runtime, err := ResolveRedisRuntime(RedisModeEmbedded, "", "", "", 0)
	if err != nil {
		t.Fatalf("ResolveRedisRuntime returned error: %v", err)
	}
	defer runtime.Cleanup()

	if runtime.Mode != RedisModeEmbedded {
		t.Fatalf("expected mode %q, got %q", RedisModeEmbedded, runtime.Mode)
	}
	if runtime.Address == "" {
		t.Fatalf("expected embedded redis address")
	}

	client := asynq.NewClient(runtime.ConnOpt)
	defer client.Close()
	if pingErr := client.Ping(); pingErr != nil {
		t.Fatalf("expected embedded redis ping to succeed: %v", pingErr)
	}
}

func TestResolveRedisRuntimeAutoFallbackToEmbedded(t *testing.T) {
	runtime, err := ResolveRedisRuntime(RedisModeAuto, "", "127.0.0.1:1", "", 0)
	if err != nil {
		t.Fatalf("ResolveRedisRuntime returned error: %v", err)
	}
	defer runtime.Cleanup()

	if runtime.Mode != RedisModeEmbedded {
		t.Fatalf("expected auto mode to fallback to %q, got %q", RedisModeEmbedded, runtime.Mode)
	}
	if runtime.FallbackReason == nil {
		t.Fatalf("expected fallback reason to be set")
	}
}

func TestResolveRedisRuntimeExternalReturnsExternalConnOpt(t *testing.T) {
	runtime, err := ResolveRedisRuntime(RedisModeExternal, "", "127.0.0.1:1", "", 0)
	if err != nil {
		t.Fatalf("ResolveRedisRuntime returned error: %v", err)
	}
	defer runtime.Cleanup()

	if runtime.Mode != RedisModeExternal {
		t.Fatalf("expected mode %q, got %q", RedisModeExternal, runtime.Mode)
	}
}

func TestResolveRedisRuntimeInvalidMode(t *testing.T) {
	_, err := ResolveRedisRuntime("invalid", "", "127.0.0.1:6379", "", 0)
	if err == nil {
		t.Fatalf("expected invalid mode error")
	}
}
