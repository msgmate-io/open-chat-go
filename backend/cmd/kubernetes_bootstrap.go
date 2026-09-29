//go:build kubernetesintegration

package cmd

import (
	"errors"

	"backend/integrationsettings"

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

	// Register the deployment config persister: when a kubernetes cluster is
	// marked as the deployment host, admin config writes are persisted into its
	// config Secret (and the workload restarted) instead of / in addition to
	// the local config file. This decouples the Open-Chat config from the Helm
	// release.
	integrationsettings.RegisterRemoteConfigPersister(func(data []byte) (string, error) {
		target, err := kubernetesintegration.PersistDeploymentConfig(DB, fallbackOwner, data)
		if errors.Is(err, kubernetesintegration.ErrNoDeploymentHost) {
			return "", integrationsettings.ErrNoRemoteConfigTarget
		}
		return target, err
	})
	return nil
}
