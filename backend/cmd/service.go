package cmd

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"
)

const (
	// ServiceName is the stable identifier used by every OS service manager.
	ServiceName = "open-chat"
	// ServiceDisplayName is the human readable service name.
	ServiceDisplayName = "Open Chat"
	// ServiceDescription describes the managed service.
	ServiceDescription = "Open Chat server and background worker"
)

// serviceRunConfig returns the kardianos configuration used when the process
// runs as a service. Install builds a richer config with Executable/Arguments;
// this one is sufficient for service.New + Service.Run.
func serviceRunConfig() *service.Config {
	return &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	}
}

// serviceProgram adapts runServer to the kardianos service.Interface contract.
// Start launches the server in the background and returns immediately; Stop
// cancels the server context and waits for it to unwind.
type serviceProgram struct {
	run  func(context.Context) error
	stop context.CancelFunc
	done chan error
}

func (p *serviceProgram) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.stop = cancel
	p.done = make(chan error, 1)
	go func() {
		p.done <- p.run(ctx)
	}()
	return nil
}

func (p *serviceProgram) Stop(s service.Service) error {
	if p.stop != nil {
		p.stop()
	}
	if p.done == nil {
		return nil
	}
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
	}
	return nil
}

// ServiceRunCli exposes the hidden `service run` entrypoint that the OS service
// manager invokes. It is intentionally hidden from the top-level help.
func ServiceRunCli() *cli.Command {
	return &cli.Command{
		Name:   "service",
		Usage:  "internal service entrypoint used by the OS service manager",
		Hidden: true,
		Commands: []*cli.Command{
			{
				Name:   "run",
				Usage:  "run the Open Chat server under the OS service manager",
				Flags:  GetServerFlags(),
				Hidden: true,
				Action: func(ctx context.Context, c *cli.Command) error {
					return runService(ctx, c)
				},
			},
		},
	}
}

func runService(ctx context.Context, c *cli.Command) error {
	program := &serviceProgram{
		run: func(runCtx context.Context) error {
			return runServer(runCtx, c)
		},
	}

	s, err := service.New(program, serviceRunConfig())
	if err != nil {
		if errors.Is(err, service.ErrNoServiceSystemDetected) {
			return errors.New("no supported service manager detected on this host (systemd, launchd, Windows SCM, ...)")
		}
		return err
	}

	if service.Interactive() {
		log.Printf("Running Open Chat as a service in the foreground; press Ctrl+C to stop")
	}

	return s.Run()
}
