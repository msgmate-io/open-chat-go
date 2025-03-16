package queue

import (
	"context"

	wsapi "backend/api/websocket"
	"backend/queue/tasks"
	"backend/workqueue"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type Processor struct {
	DB          *gorm.DB
	BackendHost string
	WSHandler   *wsapi.WebSocketHandler
}

func (p *Processor) NewServeMux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	deps := p.deps()

	mux.HandleFunc(TypeToolExecution, func(ctx context.Context, task *asynq.Task) error {
		return tasks.HandleToolExecution(ctx, task, deps)
	})
	mux.HandleFunc(workqueue.TypeBotReply, func(ctx context.Context, task *asynq.Task) error {
		return tasks.HandleBotReply(ctx, task, deps)
	})
	return mux
}

func (p *Processor) deps() tasks.Deps {
	return tasks.Deps{
		DB:          p.DB,
		BackendHost: p.BackendHost,
		WSHandler:   p.WSHandler,
	}
}
