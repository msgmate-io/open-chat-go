//go:build docker_sandbox

package cmd

import (
	"fmt"

	dockersandboxintegration "github.com/msgmate-io/docker-sandbox-integration"
	"gorm.io/gorm"
)

// applyDockerSandboxBootstrapSources applies the docker_sandbox integration's
// bootstrap spec (bootstrap.docker_sandbox.sandboxes): each declared sandbox is
// upserted by (owner, name) and, when requested, started asynchronously.
func applyDockerSandboxBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, sandboxSpecs []string) error {
	all := make([]dockersandboxintegration.BootstrapSandboxSpec, 0)
	for idx, spec := range sandboxSpecs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return fmt.Errorf("add-docker-sandboxes-from-config[%d]: %w", idx, err)
		}
		decoded, err := dockersandboxintegration.DecodeBootstrapSandboxes(raw)
		if err != nil {
			return fmt.Errorf("add-docker-sandboxes-from-config[%d] (%s): %w", idx, source, err)
		}
		all = append(all, decoded...)
	}

	owners := normalizeOwnersList(defaultOwners)
	if len(all) == 0 && len(owners) == 0 {
		return nil
	}

	if _, err := dockersandboxintegration.ApplyBootstrap(DB, dockersandboxintegration.BootstrapSpec{
		FallbackOwner: fallbackOwner,
		DefaultOwners: owners,
		Sandboxes:     all,
	}); err != nil {
		return err
	}
	return nil
}
