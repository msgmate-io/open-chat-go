package queue

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/workqueue"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

// TestTwoWorkerDrainDistinctChats runs two independent asynq servers against a
// single queue (the decoupled-worker-tier topology) and verifies that a burst
// of distinct per-chat bot reply tasks is drained exactly once each: 50
// invocations, 50 unique task IDs, no duplicates (ci#648).
func TestTwoWorkerDrainDistinctChats(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	connOpt := asynq.RedisClientOpt{Addr: mr.Addr()}

	client := asynq.NewClient(connOpt)
	defer client.Close()

	const chatCount = 50

	var mu sync.Mutex
	seen := make([]string, 0, chatCount)

	// Two workers sharing one queue, each with concurrency 2.
	newServer := func() (*asynq.Server, *asynq.ServeMux) {
		server := asynq.NewServer(connOpt, asynq.Config{
			Concurrency: 2,
			Queues:      map[string]int{QueueDefault: 1},
		})
		mux := asynq.NewServeMux()
		mux.HandleFunc(workqueue.TypeBotReply, func(_ context.Context, task *asynq.Task) error {
			mu.Lock()
			seen = append(seen, task.ResultWriter().TaskID())
			mu.Unlock()
			return nil
		})
		return server, mux
	}

	serverA, muxA := newServer()
	serverB, muxB := newServer()

	if err := serverA.Start(muxA); err != nil {
		t.Fatalf("start worker A: %v", err)
	}
	defer serverA.Shutdown()
	if err := serverB.Start(muxB); err != nil {
		t.Fatalf("start worker B: %v", err)
	}
	defer serverB.Shutdown()

	for i := 0; i < chatCount; i++ {
		payload := workqueue.BotReplyPayload{
			ChatUUID:    fmt.Sprintf("chat-%d", i),
			MessageUUID: fmt.Sprintf("msg-%d", i),
			BotUserID:   1,
		}
		if _, err := workqueue.EnqueueBotReply(client, nil, payload); err != nil {
			if isUnsupportedMiniredisError(err) {
				t.Skipf("miniredis cannot run asynq scripts: %v", err)
			}
			t.Fatalf("enqueue chat-%d: %v", i, err)
		}
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= chatCount {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out draining queue: processed %d/%d", n, chatCount)
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()

	if len(got) != chatCount {
		t.Fatalf("expected %d invocations, got %d", chatCount, len(got))
	}

	unique := make(map[string]struct{}, len(got))
	for _, id := range got {
		unique[id] = struct{}{}
	}
	if len(unique) != chatCount {
		t.Fatalf("expected %d unique task IDs, got %d (duplicate work was processed)", chatCount, len(unique))
	}
}

func isUnsupportedMiniredisError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "script") ||
		strings.Contains(msg, "eval") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "not supported")
}
