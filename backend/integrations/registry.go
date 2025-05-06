package integrations

import (
	_ "backend/integrations/externalintegrations"
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"

	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"
)

var (
	registryMu sync.RWMutex
	loaded     bool
	registry   = map[string]extiface.Definition{}
)

func normalizeIntegrationName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func normalizeFrontendRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "" {
		return ""
	}
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	if route != "/" && strings.HasSuffix(route, "/") {
		route = strings.TrimSuffix(route, "/")
	}
	return route
}

func expectedIntegrationFrontendPrefix(name string) string {
	return "/integrations/" + normalizeIntegrationName(name)
}

func normalizeFrontendAssetPath(assetPath string) string {
	assetPath = strings.TrimSpace(assetPath)
	if assetPath == "" {
		return ""
	}
	assetPath = strings.TrimPrefix(assetPath, "/")
	assetPath = path.Clean(assetPath)
	if assetPath == "." {
		return ""
	}
	if strings.HasPrefix(assetPath, "../") || assetPath == ".." {
		return ""
	}
	return assetPath
}

func validateAndNormalizeDefinition(def extiface.Definition) (extiface.Definition, error) {
	name := normalizeIntegrationName(def.Name)
	if name == "" {
		return def, fmt.Errorf("integration name is required")
	}
	def.Name = name

	frontendRoutes := make([]extiface.FrontendRoute, 0, len(def.FrontendRoutes))
	for idx, route := range def.FrontendRoutes {
		routePath := normalizeFrontendRoute(route.Route)
		if routePath == "" {
			return def, fmt.Errorf("integration %q frontend_routes[%d] is missing route", name, idx)
		}
		if strings.Contains(routePath, " ") {
			return def, fmt.Errorf("integration %q frontend route %q must not include method prefixes", name, routePath)
		}
		if strings.HasPrefix(routePath, "/api/") || routePath == "/api" {
			return def, fmt.Errorf("integration %q frontend route %q cannot use /api prefix", name, routePath)
		}
		expectedPrefix := expectedIntegrationFrontendPrefix(name)
		if routePath != expectedPrefix && !strings.HasPrefix(routePath, expectedPrefix+"/") {
			return def, fmt.Errorf("integration %q frontend route %q must be under %q", name, routePath, expectedPrefix)
		}
		if route.Handler == nil {
			return def, fmt.Errorf("integration %q frontend route %q is missing handler", name, routePath)
		}
		route.Route = routePath
		frontendRoutes = append(frontendRoutes, route)
	}
	def.FrontendRoutes = frontendRoutes

	frontendPages := make([]extiface.FrontendPage, 0, len(def.FrontendPages))
	if len(def.FrontendPages) > 0 && def.FrontendAssets == nil {
		return def, fmt.Errorf("integration %q defines frontend pages but no frontend assets filesystem", name)
	}
	for idx, page := range def.FrontendPages {
		routePath := normalizeFrontendRoute(page.Route)
		if routePath == "" {
			return def, fmt.Errorf("integration %q frontend_pages[%d] is missing route", name, idx)
		}
		if strings.Contains(routePath, " ") {
			return def, fmt.Errorf("integration %q frontend page route %q must not include method prefixes", name, routePath)
		}
		if strings.HasPrefix(routePath, "/api/") || routePath == "/api" {
			return def, fmt.Errorf("integration %q frontend page route %q cannot use /api prefix", name, routePath)
		}
		expectedPrefix := expectedIntegrationFrontendPrefix(name)
		if routePath != expectedPrefix && !strings.HasPrefix(routePath, expectedPrefix+"/") {
			return def, fmt.Errorf("integration %q frontend page route %q must be under %q", name, routePath, expectedPrefix)
		}
		assetPath := normalizeFrontendAssetPath(page.AssetPath)
		if assetPath == "" {
			return def, fmt.Errorf("integration %q frontend page route %q has invalid asset path %q", name, routePath, page.AssetPath)
		}
		page.Route = routePath
		page.AssetPath = assetPath
		frontendPages = append(frontendPages, page)
	}
	def.FrontendPages = frontendPages

	return def, nil
}

func EnsureLoaded() {
	registryMu.Lock()
	defer registryMu.Unlock()
	if loaded {
		return
	}
	registry = map[string]extiface.Definition{}
	for _, def := range extiface.List() {
		normalizedDef, err := validateAndNormalizeDefinition(def)
		if err != nil {
			log.Fatalf("invalid integration definition %q: %v", def.Name, err)
		}
		name := normalizedDef.Name
		if name == "" {
			continue
		}
		registry[name] = normalizedDef
	}
	loaded = true
}

func List() []extiface.Definition {
	EnsureLoaded()
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]extiface.Definition, 0, len(registry))
	for _, def := range registry {
		out = append(out, def)
	}
	return out
}

func Has(name string) bool {
	EnsureLoaded()
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[normalizeIntegrationName(name)]
	return ok
}

func Get(name string) (extiface.Definition, bool) {
	EnsureLoaded()
	registryMu.RLock()
	defer registryMu.RUnlock()
	def, ok := registry[normalizeIntegrationName(name)]
	return def, ok
}

func Call(name string, functionName string, ctx context.Context, payload map[string]interface{}) (interface{}, error) {
	EnsureLoaded()
	registryMu.RLock()
	defer registryMu.RUnlock()
	def, ok := registry[normalizeIntegrationName(name)]
	if !ok {
		return nil, fmt.Errorf("integration %q not registered", name)
	}
	fn, ok := def.Functions[strings.TrimSpace(functionName)]
	if !ok || fn == nil {
		return nil, fmt.Errorf("integration %q has no function %q", name, functionName)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	return fn(ctx, payload)
}

func RegisterRoutes(v1Private *http.ServeMux, root *http.ServeMux) {
	EnsureLoaded()
	for _, def := range List() {
		if def.RouteRegistrar != nil {
			def.RouteRegistrar(v1Private, root)
		}
	}
}

func RegisterFrontendRoutes(root *http.ServeMux, wrapper func(http.Handler, bool) http.Handler) error {
	EnsureLoaded()
	if root == nil {
		return fmt.Errorf("root mux is required")
	}

	routeOwners := map[string]string{}
	for _, def := range List() {
		for _, frontendPage := range def.FrontendPages {
			frontendPage := frontendPage
			if owner, exists := routeOwners[frontendPage.Route]; exists {
				return fmt.Errorf("duplicate integration frontend route %q from %q and %q", frontendPage.Route, owner, def.Name)
			}
			routeOwners[frontendPage.Route] = def.Name

			content, err := fs.ReadFile(def.FrontendAssets, frontendPage.AssetPath)
			if err != nil {
				return fmt.Errorf("integration %q frontend page %q failed to read asset %q: %w", def.Name, frontendPage.Route, frontendPage.AssetPath, err)
			}

			h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(content)
			}))
			if wrapper != nil {
				h = wrapper(h, frontendPage.Public)
			}
			root.Handle(frontendPage.Route, h)
		}

		for _, frontendRoute := range def.FrontendRoutes {
			frontendRoute := frontendRoute
			if owner, exists := routeOwners[frontendRoute.Route]; exists {
				return fmt.Errorf("duplicate integration frontend route %q from %q and %q", frontendRoute.Route, owner, def.Name)
			}
			routeOwners[frontendRoute.Route] = def.Name

			h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				frontendRoute.Handler(w, r)
			}))
			if wrapper != nil {
				h = wrapper(h, frontendRoute.Public)
			}
			root.Handle(frontendRoute.Route, h)
		}
	}

	return nil
}

func AdditionalModels() []interface{} {
	EnsureLoaded()
	models := []interface{}{}
	for _, def := range List() {
		for _, provider := range def.ModelProviders {
			if provider == nil {
				continue
			}
			models = append(models, provider()...)
		}
	}
	return models
}
