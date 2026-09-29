package integrationsettings

import (
	"errors"
	"sync"
)

// ErrNoRemoteConfigTarget indicates that no remote config backing store is
// configured (e.g. no kubernetes deployment-host cluster is marked).
var ErrNoRemoteConfigTarget = errors.New("no remote config target is configured")

// RemoteConfigPersister persists the Open-Chat config document to an external
// backing store (for example a Kubernetes Secret on the deployment-host
// cluster). It returns a human-readable target description. Implementations
// should return ErrNoRemoteConfigTarget when nothing is configured.
type RemoteConfigPersister func(data []byte) (string, error)

var (
	remoteConfigMu        sync.RWMutex
	remoteConfigPersister RemoteConfigPersister
)

// RegisterRemoteConfigPersister installs the process-wide remote config
// persister. Passing nil clears it. Safe to call repeatedly (e.g. on every
// startup bootstrap).
func RegisterRemoteConfigPersister(p RemoteConfigPersister) {
	remoteConfigMu.Lock()
	remoteConfigPersister = p
	remoteConfigMu.Unlock()
}

// HasRemoteConfigPersister reports whether a remote config persister is
// registered.
func HasRemoteConfigPersister() bool {
	remoteConfigMu.RLock()
	defer remoteConfigMu.RUnlock()
	return remoteConfigPersister != nil
}

// PersistRemoteConfig writes the config document to the registered remote
// backing store. When no persister is registered it returns ("", false, nil).
// When a persister is registered but no target is configured it returns
// ("", false, ErrNoRemoteConfigTarget). On success it returns (target, true, nil).
func PersistRemoteConfig(data []byte) (string, bool, error) {
	remoteConfigMu.RLock()
	p := remoteConfigPersister
	remoteConfigMu.RUnlock()
	if p == nil {
		return "", false, nil
	}
	target, err := p(data)
	if err != nil {
		if errors.Is(err, ErrNoRemoteConfigTarget) {
			return "", false, err
		}
		return target, true, err
	}
	return target, true, nil
}
