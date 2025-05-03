package integrations

import (
	backendintegrations "backend/integrations"
	"encoding/json"
	"net/http"
	"sort"
)

type IntegrationListRow struct {
	Name               string `json:"name"`
	HasRouteRegistrar  bool   `json:"has_route_registrar"`
	APIRouteCount      int    `json:"api_route_count"`
	ModelProviderCount int    `json:"model_provider_count"`
	FunctionCount      int    `json:"function_count"`
}

type IntegrationsListResponse struct {
	Rows []IntegrationListRow `json:"rows"`
}

// List returns integrations compiled into this backend binary.
//
//	@Summary      List compiled integrations
//	@Description  Returns integration modules compiled into the running backend binary.
//	@Tags         tools
//	@Produce      json
//	@Success      200 {object} IntegrationsListResponse
//	@Router       /api/v1/integrations/list [get]
func (h *IntegrationsHandler) List(w http.ResponseWriter, r *http.Request) {
	defs := backendintegrations.List()
	rows := make([]IntegrationListRow, 0, len(defs))
	for _, def := range defs {
		rows = append(rows, IntegrationListRow{
			Name:               def.Name,
			HasRouteRegistrar:  def.RouteRegistrar != nil,
			APIRouteCount:      len(def.APIRoutes),
			ModelProviderCount: len(def.ModelProviders),
			FunctionCount:      len(def.Functions),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(IntegrationsListResponse{Rows: rows})
}
