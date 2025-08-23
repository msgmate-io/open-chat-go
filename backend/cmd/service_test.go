package cmd

import (
	"context"
	"testing"
	"time"
)

func TestServiceProgramStartStop(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})

	prg := &serviceProgram{
		run: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(released)
			return nil
		},
	}

	if err := prg.Start(nil); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("run function was not started")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- prg.Stop(nil) }()

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not cancel the run context")
	}

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}

func TestServiceProgramStopWithoutStart(t *testing.T) {
	prg := &serviceProgram{}
	if err := prg.Stop(nil); err != nil {
		t.Fatalf("Stop without Start returned error: %v", err)
	}
}

func TestServiceRunConfig(t *testing.T) {
	cfg := serviceRunConfig()
	if cfg.Name != ServiceName {
		t.Fatalf("Name = %q, want %q", cfg.Name, ServiceName)
	}
	if cfg.DisplayName != ServiceDisplayName {
		t.Fatalf("DisplayName = %q, want %q", cfg.DisplayName, ServiceDisplayName)
	}
	if cfg.Description != ServiceDescription {
		t.Fatalf("Description = %q, want %q", cfg.Description, ServiceDescription)
	}
}
