//go:build !kubernetesintegration

package cmd

import (
	"fmt"

	"backend/runtimecfg"
	"gorm.io/gorm"
)

var kubernetesBootstrapEnvKeys = []string{
	"OCI_KUBERNETES_BOOTSTRAP_CLUSTERS",
	"OCI_KUBERNETES_BOOTSTRAP_CLUSTER_GRANTS",
	"OCI_KUBERNETES_BOOTSTRAP_DEFAULT_OWNERS",
}

func applyKubernetesBootstrapSources(_ *gorm.DB, _ string) error {
	for _, envKey := range kubernetesBootstrapEnvKeys {
		if value, ok := runtimecfg.GetAll()[envKey]; ok && value.Value != "" {
			return fmt.Errorf("kubernetes bootstrap requested via %s, but kubernetes integration is not included in this build", envKey)
		}
	}
	return nil
}
