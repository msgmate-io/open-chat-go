package integrations

import (
	backendintegrations "backend/integrations"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"sort"
)

type IntegrationListRow struct {
	Name               string   `json:"name"`
	HasRouteRegistrar  bool     `json:"has_route_registrar"`
	APIRouteCount      int      `json:"api_route_count"`
	FrontendRouteCount int      `json:"frontend_route_count"`
	FrontendPageCount  int      `json:"frontend_page_count"`
	FrontendDocCount   int      `json:"frontend_doc_count"`
	ModelProviderCount int      `json:"model_provider_count"`
	FunctionCount      int      `json:"function_count"`
	RuntimeEnvVarCount int      `json:"runtime_env_var_count"`
	DefaultBotCount    int      `json:"default_bot_count"`
	AdminOnly          bool     `json:"admin_only"`
	UserAccessible     bool     `json:"user_accessible"`
	Labels             []string `json:"labels"`
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
	DB, user, err := util.GetDBAndUser(r)
	if err != nil || user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	defs, err := backendintegrations.ListVisibleDefinitions(DB, user)
	if err != nil {
		http.Error(w, "Failed to resolve integration visibility", http.StatusInternalServerError)
		return
	}
	rows := make([]IntegrationListRow, 0, len(defs))
	for _, def := range defs {
		rows = append(rows, IntegrationListRow{
			Name:               def.Name,
			HasRouteRegistrar:  def.RouteRegistrar != nil,
			APIRouteCount:      len(def.APIRoutes),
			FrontendRouteCount: len(def.FrontendRoutes),
			FrontendPageCount:  len(def.FrontendPages),
			FrontendDocCount:   len(def.FrontendDocs),
			ModelProviderCount: len(def.ModelProviders),
			FunctionCount:      len(def.Functions),
			RuntimeEnvVarCount: len(def.RuntimeEnvVars),
			DefaultBotCount:    len(def.BotBootstrapConfigs),
			AdminOnly:          def.AdminOnly,
			UserAccessible:     def.UserAccessible,
			Labels:             backendintegrations.LabelsFor(def),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(IntegrationsListResponse{Rows: rows})
}
