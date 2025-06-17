//go:build !sshintegration

package cmd

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func applySSHBootstrapSources(_ *gorm.DB, _ string, defaultOwners []string, keySpecs []string, serverSpecs []string) error {
	hasOwners := false
	for _, owner := range defaultOwners {
		if strings.TrimSpace(owner) != "" {
			hasOwners = true
			break
		}
	}

	hasKeySpecs := false
	for _, spec := range keySpecs {
		if strings.TrimSpace(spec) != "" {
			hasKeySpecs = true
			break
		}
	}

	hasServerSpecs := false
	for _, spec := range serverSpecs {
		if strings.TrimSpace(spec) != "" {
			hasServerSpecs = true
			break
		}
	}

	if !hasOwners && !hasKeySpecs && !hasServerSpecs {
		return nil
	}

	return fmt.Errorf("ssh bootstrap requested, but ssh integration is not included in this build")
}
