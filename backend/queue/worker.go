package queue

import (
	"context"

	wsapi "backend/api/websocket"
	"backend/queue/drain"
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
	mux.HandleFunc(workqueue.TypeEmailAutomation, func(ctx context.Context, task *asynq.Task) error {
		return tasks.HandleEmailAutomation(ctx, task, deps)
	})
	mux.HandleFunc(workqueue.TypeIntegrationSchedule, func(ctx context.Context, task *asynq.Task) error {
		return tasks.HandleIntegrationSchedule(ctx, task, deps)
	})
	return mux
}

// BotReplyTerminalDrain exposes the terminal bot reply drain (ci#656) for the
// worker's asynq.Config, so a reply task asynq retires instead of running stops
// leaving its message rendering as queued.
func BotReplyTerminalDrain(DB *gorm.DB) asynq.ErrorHandler {
	return drain.BotReplyTerminalDrain(DB)
}

func (p *Processor) deps() tasks.Deps {
	return tasks.Deps{
		DB:          p.DB,
		BackendHost: p.BackendHost,
		WSHandler:   p.WSHandler,
	}
}
