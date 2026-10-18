package nativecoder

import (
	"context"
	"fmt"
	"sync"
)

// ConsolidationClientFactory builds the consolidation client for a repository.
// It is a package-level indirection so a test (or a different deployment) can
// supply its own client. The default builds the REST client from the process
// environment and returns (nil, nil) when nothing is configured.
var ConsolidationClientFactory = func(repo string) (ConsolidationClient, error) {
	client, err := NewGitHubAPIClientFromEnv(repo)
	if err != nil || client == nil {
		return nil, err
	}
	return client, nil
}

// SetSelfLoginForTest pins the identity `authors: [self]` resolves to, so a test
// never has to call the GitHub /user endpoint. It returns a restore function.
func SetSelfLoginForTest(login string) func() {
	previous := selfLoginOverride
	selfLoginOverride = login
	return func() { selfLoginOverride = previous }
}

var (
	selfLoginMu       sync.Mutex
	selfLoginOverride string
)

// resolveSelfLogin returns the pinned test identity when set, the deployment
// override when present, and otherwise asks the client.
func resolveSelfLogin(ctx context.Context, client ConsolidationClient) (string, error) {
	selfLoginMu.Lock()
	pinned := selfLoginOverride
	selfLoginMu.Unlock()
	if pinned != "" {
		return pinned, nil
	}
	if client == nil {
		return "", fmt.Errorf("no consolidation client")
	}
	return client.SelfLogin(ctx)
}
