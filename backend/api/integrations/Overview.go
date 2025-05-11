package integrations

import (
	backendintegrations "backend/integrations"
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

type IntegrationModelOverview struct {
	TypeName string                          `json:"type_name"`
	Kind     string                          `json:"kind"`
	Fields   []IntegrationModelFieldOverview `json:"fields,omitempty"`
}

type IntegrationModelFieldOverview struct {
	Name     string                          `json:"name"`
	JSONName string                          `json:"json_name,omitempty"`
	Type     string                          `json:"type"`
	Kind     string                          `json:"kind"`
	Required bool                            `json:"required"`
	Fields   []IntegrationModelFieldOverview `json:"fields,omitempty"`
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

type IntegrationFrontendRouteOverview struct {
	Route       string `json:"route"`
	Kind        string `json:"kind"`
	Public      bool   `json:"public"`
	Description string `json:"description,omitempty"`
	AssetPath   string `json:"asset_path,omitempty"`
}

type IntegrationOverviewResponse struct {
	Name              string                             `json:"name"`
	ReadmeMarkdown    string                             `json:"readme_markdown,omitempty"`
	APIRoutes         []string                           `json:"api_routes"`
	APIRoutesOverview []IntegrationAPIRouteOverview      `json:"api_routes_overview"`
	FrontendRoutes    []IntegrationFrontendRouteOverview `json:"frontend_routes"`
	Models            []IntegrationModelOverview         `json:"models"`
	Functions         []string                           `json:"functions"`
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

func parseJSONTag(field reflect.StructField) (string, bool, bool) {
	tag := strings.TrimSpace(field.Tag.Get("json"))
	if tag == "-" {
		return "", false, true
	}
	if tag == "" {
		return field.Name, false, false
	}
	parts := strings.Split(tag, ",")
	jsonName := strings.TrimSpace(parts[0])
	if jsonName == "" {
		jsonName = field.Name
	}
	hasOmitEmpty := false
	for _, part := range parts[1:] {
		if strings.TrimSpace(part) == "omitempty" {
			hasOmitEmpty = true
			break
		}
	}
	return jsonName, hasOmitEmpty, false
}

func shouldExpandStruct(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	if t.PkgPath() == "time" {
		return false
	}
	return true
}

func describeStructFields(t reflect.Type, seen map[reflect.Type]struct{}, depth int) []IntegrationModelFieldOverview {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return []IntegrationModelFieldOverview{}
	}
	if depth > 5 {
		return []IntegrationModelFieldOverview{}
	}
	if _, exists := seen[t]; exists {
		return []IntegrationModelFieldOverview{}
	}
	seen[t] = struct{}{}
	defer delete(seen, t)

	fields := make([]IntegrationModelFieldOverview, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}

		jsonName, hasOmitEmpty, skip := parseJSONTag(field)
		if skip {
			continue
		}

		fieldType := field.Type
		fieldKind := fieldType.Kind().String()
		required := !hasOmitEmpty && fieldType.Kind() != reflect.Ptr && fieldType.Kind() != reflect.Map && fieldType.Kind() != reflect.Slice && fieldType.Kind() != reflect.Interface

		entry := IntegrationModelFieldOverview{
			Name:     field.Name,
			JSONName: jsonName,
			Type:     fieldType.String(),
			Kind:     fieldKind,
			Required: required,
		}

		if shouldExpandStruct(fieldType) {
			entry.Fields = describeStructFields(fieldType, seen, depth+1)
		}

		fields = append(fields, entry)
	}

	sort.Slice(fields, func(i, j int) bool {
		if fields[i].JSONName == fields[j].JSONName {
			return fields[i].Name < fields[j].Name
		}
		return fields[i].JSONName < fields[j].JSONName
	})

	return fields
}

func describeModel(model interface{}) (IntegrationModelOverview, bool) {
	modelType := reflect.TypeOf(model)
	if modelType == nil {
		return IntegrationModelOverview{}, false
	}

	kind := modelType.Kind().String()
	for modelType.Kind() == reflect.Ptr {
		modelType = modelType.Elem()
	}
	typeName := modelType.Name()
	if typeName == "" {
		typeName = modelType.String()
	}

	overview := IntegrationModelOverview{TypeName: typeName, Kind: kind}
	if modelType.Kind() == reflect.Struct {
		overview.Fields = describeStructFields(modelType, map[reflect.Type]struct{}{}, 0)
	}
	return overview, true
}

func mergeAndSortRoutes(routes []string, docs []integrationinterface.APIRouteDoc) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(routes)+len(docs))
	for _, route := range routes {
		route = strings.TrimSpace(route)
		if route == "" {
			continue
		}
		if _, ok := seen[route]; ok {
			continue
		}
		seen[route] = struct{}{}
		out = append(out, route)
	}
	for _, doc := range docs {
		route := strings.TrimSpace(doc.Route)
		if route == "" {
			continue
		}
		if _, ok := seen[route]; ok {
			continue
		}
		seen[route] = struct{}{}
		out = append(out, route)
	}
	sort.Strings(out)
	return out
}

func buildAPIRouteOverview(route string, docsByRoute map[string]integrationinterface.APIRouteDoc) IntegrationAPIRouteOverview {
	doc, ok := docsByRoute[route]
	if !ok {
		return IntegrationAPIRouteOverview{
			Route:      route,
			Parameters: pathParamsFromRoute(route),
		}
	}
	params := make([]IntegrationAPIParameterOverview, 0, len(doc.Parameters))
	for _, param := range doc.Parameters {
		params = append(params, IntegrationAPIParameterOverview{
			Name:        param.Name,
			In:          param.In,
			Type:        param.Type,
			Required:    param.Required,
			Description: param.Description,
		})
	}
	if len(params) == 0 {
		params = pathParamsFromRoute(route)
	}
	return IntegrationAPIRouteOverview{
		Route:        route,
		Summary:      strings.TrimSpace(doc.Summary),
		Description:  strings.TrimSpace(doc.Description),
		RequiredAuth: append([]string(nil), doc.RequiredAuth...),
		Parameters:   params,
	}
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

	routes := mergeAndSortRoutes(append([]string(nil), def.APIRoutes...), def.APIRouteDocs)
	docsByRoute := map[string]integrationinterface.APIRouteDoc{}
	for _, routeDoc := range def.APIRouteDocs {
		route := strings.TrimSpace(routeDoc.Route)
		if route == "" {
			continue
		}
		docsByRoute[route] = routeDoc
	}

	routeOverview := make([]IntegrationAPIRouteOverview, 0, len(routes))
	for _, route := range routes {
		routeOverview = append(routeOverview, buildAPIRouteOverview(route, docsByRoute))
	}
	sort.Slice(routeOverview, func(i, j int) bool {
		return routeOverview[i].Route < routeOverview[j].Route
	})

	frontendRoutes := make([]IntegrationFrontendRouteOverview, 0, len(def.FrontendRoutes)+len(def.FrontendPages))
	for _, route := range def.FrontendRoutes {
		frontendRoutes = append(frontendRoutes, IntegrationFrontendRouteOverview{
			Route:       route.Route,
			Kind:        "handler",
			Public:      route.Public,
			Description: route.Description,
		})
	}
	for _, page := range def.FrontendPages {
		frontendRoutes = append(frontendRoutes, IntegrationFrontendRouteOverview{
			Route:       page.Route,
			Kind:        "page",
			Public:      page.Public,
			Description: page.Description,
			AssetPath:   page.AssetPath,
		})
	}
	sort.Slice(frontendRoutes, func(i, j int) bool {
		return frontendRoutes[i].Route < frontendRoutes[j].Route
	})

	models := []IntegrationModelOverview{}
	for _, provider := range def.ModelProviders {
		if provider == nil {
			continue
		}
		for _, model := range provider() {
			overview, ok := describeModel(model)
			if !ok {
				continue
			}
			models = append(models, overview)
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
		ReadmeMarkdown:    strings.TrimSpace(def.ReadmeMarkdown),
		APIRoutes:         routes,
		APIRoutesOverview: routeOverview,
		FrontendRoutes:    frontendRoutes,
		Models:            models,
		Functions:         functions,
	})
}
