package integrationsettings

import (
	"sort"
	"strings"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// IntegrationSnapshot is the settings view of a single integration.
type IntegrationSnapshot struct {
	Name       string            `json:"name"`
	FieldCount int               `json:"field_count"`
	Fields     []FieldDescriptor `json:"fields"`
}

// IntegrationSettingsListResponse is returned by the list endpoint.
type IntegrationSettingsListResponse struct {
	Deployment   DeploymentInfo        `json:"deployment"`
	Integrations []IntegrationSnapshot `json:"integrations"`
}

// IntegrationSettingsDetailResponse is returned by the detail/reveal/save
// endpoints for one integration.
type IntegrationSettingsDetailResponse struct {
	Deployment  DeploymentInfo      `json:"deployment"`
	Integration IntegrationSnapshot `json:"integration"`
}

// BuildIntegrationSnapshot builds the settings snapshot for one integration.
func BuildIntegrationSnapshot(def integrationinterface.Definition, values map[string]runtimecfg.Value, reveal bool) IntegrationSnapshot {
	fields := BuildDescriptors(def, values, reveal)
	return IntegrationSnapshot{
		Name:       def.Name,
		FieldCount: len(fields),
		Fields:     fields,
	}
}

// ListSnapshots returns snapshots for every integration that declares at least
// one RuntimeEnvVar, sorted by name.
func ListSnapshots(defs []integrationinterface.Definition, values map[string]runtimecfg.Value, reveal bool) []IntegrationSnapshot {
	out := make([]IntegrationSnapshot, 0, len(defs))
	for _, def := range defs {
		if len(def.RuntimeEnvVars) == 0 {
			continue
		}
		out = append(out, BuildIntegrationSnapshot(def, values, reveal))
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// FindDefinition returns the definition matching name (case-insensitive).
func FindDefinition(defs []integrationinterface.Definition, name string) (integrationinterface.Definition, bool) {
	target := strings.ToLower(strings.TrimSpace(name))
	for _, def := range defs {
		if strings.ToLower(def.Name) == target {
			return def, true
		}
	}
	return integrationinterface.Definition{}, false
}
