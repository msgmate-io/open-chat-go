//go:build kubernetesintegration

package cmd

import (
	kubernetesintegration "github.com/msgmate-io/kubernetes-integration"
	"gorm.io/gorm"
)

// applyKubernetesBootstrapSources applies the kubernetes integration's runtime
// config bootstrap. All kubernetes bootstrap env keys
// (OCI_KUBERNETES_BOOTSTRAP_*) are declared and owned by the kubernetes
// integration itself and can be set either directly as env vars or through
// open-chat.json (integrations.kubernetes.bootstrap_*).
func applyKubernetesBootstrapSources(DB *gorm.DB, fallbackOwner string) error {
	if _, err := kubernetesintegration.ApplyRuntimeConfigBootstrap(DB, fallbackOwner); err != nil {
		return err
	}
	return nil
}
