package workqueue

import (
	"testing"

	"github.com/hibiken/asynq"
)

// ci#724 (part 2 groundwork): the scheduling tool that will run inside a turn
// has no request scope, so it reads the queue handles this process published at
// startup. The accessors must be nil-safe and must ignore a nil argument so a
// caller can publish only the handle it owns.
func TestGlobalQueueHandlesDefaultToNilAndIgnoreNilArguments(t *testing.T) {
	client, inspector := GetGlobalQueueHandles()
	if client != nil || inspector != nil {
		t.Fatalf("expected no handles before publication, got client=%v inspector=%v", client, inspector)
	}

	SetGlobalQueueHandles(nil, nil)
	client, inspector = GetGlobalQueueHandles()
	if client != nil || inspector != nil {
		t.Fatalf("a nil publication must not create handles, got client=%v inspector=%v", client, inspector)
	}
}

func TestSetGlobalQueueHandlesPublishesBothHandles(t *testing.T) {
	connOpt := asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}
	client := asynq.NewClient(connOpt)
	defer client.Close()
	inspector := asynq.NewInspector(connOpt)
	defer inspector.Close()

	t.Cleanup(func() { SetGlobalQueueHandles(nil, nil) })
	SetGlobalQueueHandles(client, inspector)

	gotClient, gotInspector := GetGlobalQueueHandles()
	if gotClient != client {
		t.Fatalf("expected the published client back, got %v", gotClient)
	}
	if gotInspector != inspector {
		t.Fatalf("expected the published inspector back, got %v", gotInspector)
	}
}

// A later publication of only one handle must keep the other one in place
// (eg a process that owns the client but reuses an inspector created earlier).
func TestSetGlobalQueueHandlesKeepsTheUnpublishedHandle(t *testing.T) {
	connOpt := asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}
	client := asynq.NewClient(connOpt)
	defer client.Close()
	inspector := asynq.NewInspector(connOpt)
	defer inspector.Close()

	t.Cleanup(func() { SetGlobalQueueHandles(nil, nil) })
	SetGlobalQueueHandles(client, inspector)
	SetGlobalQueueHandles(client, nil)

	gotClient, gotInspector := GetGlobalQueueHandles()
	if gotClient != client {
		t.Fatalf("expected the client to survive, got %v", gotClient)
	}
	if gotInspector != inspector {
		t.Fatalf("expected the inspector to survive, got %v", gotInspector)
	}
}
