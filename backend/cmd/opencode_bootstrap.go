//go:build opencodeintegration

package cmd

import (
	"fmt"

	opencodeintegration "github.com/msgmate-io/opencode-integration"
	"gorm.io/gorm"
)

func applyOpencodeBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, projectSpecs []string) error {
	allProjectSpecs := make([]opencodeintegration.BootstrapProjectSpec, 0)
	for idx, spec := range projectSpecs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return fmt.Errorf("add-opencode-projects-from-config[%d]: %w", idx, err)
		}
		decoded, err := decodeOneOrManyJSON[opencodeintegration.BootstrapProjectSpec](raw, source)
		if err != nil {
			return fmt.Errorf("add-opencode-projects-from-config[%d]: %w", idx, err)
		}
		allProjectSpecs = append(allProjectSpecs, decoded...)
	}

	if len(allProjectSpecs) == 0 {
		return nil
	}

	_, err := opencodeintegration.ApplyBootstrap(DB, opencodeintegration.BootstrapSpec{
		FallbackOwner: fallbackOwner,
		DefaultOwners: normalizeOwnersList(defaultOwners),
		Projects:      allProjectSpecs,
	})
	if err != nil {
		return err
	}

	return nil
}
