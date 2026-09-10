//go:build !opencodeintegration

package cmd

import (
	"fmt"
	"strings"

	"backend/runtimecfg"
	"gorm.io/gorm"
)

func runtimeEnvLookup(envKey string) (string, bool) {
	value, ok := runtimecfg.GetAll()[envKey]
	if !ok {
		return "", false
	}
	return value.Value, true
}

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
		for _, envKey := range []string{"OCI_OPENCODE_BOOTSTRAP_PROJECTS", "OCI_OPENCODE_BOOTSTRAP_DEFAULT_OWNERS"} {
			if value, ok := runtimeEnvLookup(envKey); ok && value != "" {
				return fmt.Errorf("opencode bootstrap requested via %s, but opencode integration is not included in this build", envKey)
			}
		}
		return nil
	}

	return fmt.Errorf("opencode bootstrap requested, but opencode integration is not included in this build")
}
