//go:build !docker_sandbox

package cmd

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// applyDockerSandboxBootstrapSources is the stub used when the docker_sandbox
// integration is not included in the build. A configured bootstrap then fails
// loudly instead of being silently ignored.
func applyDockerSandboxBootstrapSources(_ *gorm.DB, _ string, defaultOwners []string, sandboxSpecs []string) error {
	for _, spec := range append(append([]string{}, defaultOwners...), sandboxSpecs...) {
		if strings.TrimSpace(spec) != "" {
			return fmt.Errorf("docker_sandbox bootstrap requested via bootstrap.docker_sandbox, but the docker_sandbox integration is not included in this build")
		}
	}
	return nil
}
