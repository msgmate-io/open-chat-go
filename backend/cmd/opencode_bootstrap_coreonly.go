//go:build !opencodeintegration

package cmd

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func applyOpencodeBootstrapSources(_ *gorm.DB, _ string, defaultOwners []string, projectSpecs []string) error {
	hasOwners := false
	for _, owner := range defaultOwners {
		if strings.TrimSpace(owner) != "" {
			hasOwners = true
			break
		}
	}

	hasProjectSpecs := false
	for _, spec := range projectSpecs {
		if strings.TrimSpace(spec) != "" {
			hasProjectSpecs = true
			break
		}
	}

	if !hasOwners && !hasProjectSpecs {
		return nil
	}

	return fmt.Errorf("opencode bootstrap requested, but opencode integration is not included in this build")
}
