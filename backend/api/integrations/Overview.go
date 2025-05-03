package integrations

import (
	backendintegrations "backend/integrations"
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type IntegrationModelOverview struct {
	TypeName string `json:"type_name"`
	Kind     string `json:"kind"`
}

type IntegrationAPIParameterOverview struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type IntegrationAPIRouteOverview struct {
	Route        string                            `json:"route"`
	Summary      string                            `json:"summary,omitempty"`
	Description  string                            `json:"description,omitempty"`
	RequiredAuth []string                          `json:"required_auth,omitempty"`
	Parameters   []IntegrationAPIParameterOverview `json:"parameters,omitempty"`
}

type IntegrationOverviewResponse struct {
	Name              string                        `json:"name"`
	APIRoutes         []string                      `json:"api_routes"`
	APIRoutesOverview []IntegrationAPIRouteOverview `json:"api_routes_overview"`
	Models            []IntegrationModelOverview    `json:"models"`
	Functions         []string                      `json:"functions"`
}

func pathParamsFromRoute(route string) []IntegrationAPIParameterOverview {
	idx := strings.Index(route, " ")
	if idx < 0 || idx+1 >= len(route) {
		return []IntegrationAPIParameterOverview{}
	}
	path := strings.TrimSpace(route[idx+1:])
	matches := regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1)
	params := make([]IntegrationAPIParameterOverview, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" {
			continue
		}
		params = append(params, IntegrationAPIParameterOverview{
			Name:     name,
			In:       "path",
			Type:     "string",
			Required: true,
		})
	}
	return params
}

// Overview returns API, model and function overview for one compiled integration.
//
//	@Summary      Get integration overview
//	@Description  Returns API routes, registered model types and function names for a compiled integration.
//	@Tags         tools
//	@Produce      json
//	@Param        integration_name path string true "Integration name"
//	@Success      200 {object} IntegrationOverviewResponse
//	@Failure      404 {string} string "integration not found"
//	@Router       /api/v1/integrations/{integration_name}/overview [get]
func (h *IntegrationsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	integrationName := strings.ToLower(strings.TrimSpace(r.PathValue("integration_name")))
	if integrationName == "" {
		http.Error(w, "integration_name is required", http.StatusBadRequest)
		return
	}

	def, found := backendintegrations.Get(integrationName)
	if !found {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}

	routes := append([]string(nil), def.APIRoutes...)
	sort.Strings(routes)

	routeOverview := make([]IntegrationAPIRouteOverview, 0, len(routes))
	for _, route := range routes {
		routeOverview = append(routeOverview, IntegrationAPIRouteOverview{
			Route:      route,
			Parameters: pathParamsFromRoute(route),
		})
	}
	sort.Slice(routeOverview, func(i, j int) bool {
		return routeOverview[i].Route < routeOverview[j].Route
	})

	models := []IntegrationModelOverview{}
	for _, provider := range def.ModelProviders {
		if provider == nil {
			continue
		}
		for _, model := range provider() {
			modelType := reflect.TypeOf(model)
			if modelType == nil {
				continue
			}
			kind := modelType.Kind().String()
			if modelType.Kind() == reflect.Ptr {
				modelType = modelType.Elem()
			}
			typeName := modelType.Name()
			if typeName == "" {
				typeName = modelType.String()
			}
			models = append(models, IntegrationModelOverview{TypeName: typeName, Kind: kind})
		}
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].TypeName == models[j].TypeName {
			return models[i].Kind < models[j].Kind
		}
		return models[i].TypeName < models[j].TypeName
	})

	functions := make([]string, 0, len(def.Functions))
	for name := range def.Functions {
		functions = append(functions, name)
	}
	sort.Strings(functions)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(IntegrationOverviewResponse{
		Name:              def.Name,
		APIRoutes:         routes,
		APIRoutesOverview: routeOverview,
		Models:            models,
		Functions:         functions,
	})
}
