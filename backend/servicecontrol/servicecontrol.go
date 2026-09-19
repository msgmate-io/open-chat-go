// Package servicecontrol exposes OS service-manager operations without
// importing backend/cmd. It is a leaf package so both cmd and integration
// handlers can use it without creating an import cycle.
package servicecontrol

import (
	"github.com/kardianos/service"
)

const (
	// Name is the stable identifier used by every OS service manager.
	Name = "open-chat"
	// DisplayName is the human readable service name.
	DisplayName = "Open Chat"
	// Description describes the managed service.
	Description = "Open Chat server and background worker"
)

type noopProgram struct{}

func (noopProgram) Start(service.Service) error { return nil }
func (noopProgram) Stop(service.Service) error  { return nil }

func serviceHandle() (service.Service, error) {
	return service.New(noopProgram{}, &service.Config{
		Name:        Name,
		DisplayName: DisplayName,
		Description: Description,
	})
}

// Status reports whether the open-chat OS service is installed and running.
// A missing service is not an error: installed is false and err is nil.
func Status() (installed bool, running bool, err error) {
	s, err := serviceHandle()
	if err != nil {
		return false, false, err
	}
	status, statusErr := s.Status()
	if statusErr != nil {
		return false, false, nil
	}
	switch status {
	case service.StatusRunning:
		return true, true, nil
	case service.StatusStopped:
		return true, false, nil
	default:
		return true, false, nil
	}
}

// RestartSupported reports whether a self-restart can be attempted, i.e. the
// service is installed and manageable by the current process.
func RestartSupported() bool {
	installed, _, err := Status()
	return err == nil && installed
}

// Restart asks the OS service manager to restart the open-chat service.
func Restart() error {
	s, err := serviceHandle()
	if err != nil {
		return err
	}
	return s.Restart()
}
