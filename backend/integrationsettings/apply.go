package integrationsettings

import (
	"errors"
	"strings"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// ConfigDefinitions returns the registered integration definitions together
// with the synthetic core definition. It mirrors what the admin settings API
// exposes for editing so raw config validation/redaction is consistent between
// the HTTP layer and integration tools.
func ConfigDefinitions() []integrationinterface.Definition {
	defs := integrationinterface.List()
	defs = append(defs, CoreDefinition())
	return defs
}

// ApplyValidationError carries the per-field validation problems of a rejected
// raw config document.
type ApplyValidationError struct {
	Errors []string
}

func (e *ApplyValidationError) Error() string {
	return strings.Join(e.Errors, "; ")
}

// ApplyResult reports how a raw config document was persisted.
type ApplyResult struct {
	Deployment      DeploymentInfo
	Persisted       bool
	PersistError    string
	RestartRequired bool
	RemotePersisted bool
	RemoteTarget    string
	RemoteError     string
}

// ApplyRawConfigDocument validates a raw config document and persists it to the
// local config source, restoring values that were sent back using the redaction
// sentinel. When a deployment-host kubernetes Secret is configured the same
// document is mirrored there. A read-only local mount is tolerated as long as
// the remote mirror succeeds.
//
// Validation failures are returned as *ApplyValidationError. Parse and other
// hard failures are returned as plain errors.
func ApplyRawConfigDocument(source string, yaml string) (ApplyResult, error) {
	defs := ConfigDefinitions()

	root, err := ParseConfigDocument(yaml)
	if err != nil {
		return ApplyResult{}, err
	}
	if errs := ValidateConfigDocument(root, defs); len(errs) > 0 {
		return ApplyResult{}, &ApplyValidationError{Errors: errs}
	}

	result := ApplyResult{
		Deployment:      BuildDeploymentInfo(),
		RestartRequired: true,
	}

	if original, sourceFormat, err := LoadConfigDocument(source); err != nil {
		result.PersistError = err.Error()
		return result, nil
	} else {
		restored := RestoreRedactedConfig(original, root, runtimecfg.GetAll(), defs)
		if _, err := SaveConfigDocument(source, restored); err != nil {
			result.PersistError = err.Error()
		} else {
			result.Persisted = true
		}

		if encoded, encErr := EncodeConfigDocument(restored, sourceFormat); encErr != nil {
			result.RemoteError = encErr.Error()
		} else if target, configured, remoteErr := PersistRemoteConfig(encoded); remoteErr != nil {
			if !errors.Is(remoteErr, ErrNoRemoteConfigTarget) {
				result.RemoteError = remoteErr.Error()
			}
		} else if configured {
			result.RemotePersisted = true
			result.RemoteTarget = target
			result.Persisted = true
		}
	}

	return result, nil
}
