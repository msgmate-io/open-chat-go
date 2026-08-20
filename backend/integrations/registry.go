package integrations

import (
	_ "backend/integrations/externalintegrations"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path"
	"sort"
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

func normalizeRuntimeEnvKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}

func normalizeRuntimeConfigAliasKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
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

	runtimeEnvVars := make([]extiface.RuntimeEnvVar, 0, len(def.RuntimeEnvVars))
	runtimeEnvByKey := map[string]struct{}{}
	for idx, envVar := range def.RuntimeEnvVars {
		key := normalizeRuntimeEnvKey(envVar.Key)
		if key == "" {
			return def, fmt.Errorf("integration %q runtime_env_vars[%d] is missing key", name, idx)
		}
		if !strings.HasPrefix(key, "OCI_") {
			return def, fmt.Errorf("integration %q runtime env key %q must use OCI_ prefix", name, key)
		}
		if _, exists := runtimeEnvByKey[key]; exists {
			return def, fmt.Errorf("integration %q has duplicate runtime env key %q", name, key)
		}
		envVar.Key = key
		runtimeEnvVars = append(runtimeEnvVars, envVar)
		runtimeEnvByKey[key] = struct{}{}
	}
	def.RuntimeEnvVars = runtimeEnvVars

	runtimeConfigAliases := make([]extiface.RuntimeConfigAlias, 0, len(def.RuntimeConfigAliases))
	aliasByJSONKey := map[string]struct{}{}
	for idx, alias := range def.RuntimeConfigAliases {
		jsonKey := normalizeRuntimeConfigAliasKey(alias.JSONKey)
		if jsonKey == "" {
			return def, fmt.Errorf("integration %q runtime_config_aliases[%d] is missing json_key", name, idx)
		}
		envKey := normalizeRuntimeEnvKey(alias.EnvKey)
		if envKey == "" {
			return def, fmt.Errorf("integration %q runtime_config_aliases[%d] is missing env_key", name, idx)
		}
		if !strings.HasPrefix(envKey, "OCI_") {
			return def, fmt.Errorf("integration %q runtime config alias env_key %q must use OCI_ prefix", name, envKey)
		}
		if _, exists := runtimeEnvByKey[envKey]; !exists {
			return def, fmt.Errorf("integration %q runtime config alias %q references undeclared runtime env key %q", name, jsonKey, envKey)
		}
		if _, exists := aliasByJSONKey[jsonKey]; exists {
			return def, fmt.Errorf("integration %q has duplicate runtime config alias json_key %q", name, jsonKey)
		}
		alias.JSONKey = jsonKey
		alias.EnvKey = envKey
		runtimeConfigAliases = append(runtimeConfigAliases, alias)
		aliasByJSONKey[jsonKey] = struct{}{}
	}
	def.RuntimeConfigAliases = runtimeConfigAliases

	return def, nil
}

func EnsureLoaded() {
	registryMu.Lock()
	defer registryMu.Unlock()
	if loaded {
		return
	}
	registry = map[string]extiface.Definition{}
	runtimeEnvOwners := map[string]string{}
	for _, def := range extiface.List() {
		normalizedDef, err := validateAndNormalizeDefinition(def)
		if err != nil {
			log.Fatalf("invalid integration definition %q: %v", def.Name, err)
		}
		name := normalizedDef.Name
		if name == "" {
			continue
		}
		for _, envVar := range normalizedDef.RuntimeEnvVars {
			if owner, exists := runtimeEnvOwners[envVar.Key]; exists {
				log.Fatalf("duplicate integration runtime env key %q from %q and %q", envVar.Key, owner, name)
			}
			runtimeEnvOwners[envVar.Key] = name
		}
		registry[name] = normalizedDef
	}
	loaded = true
}

type RuntimeEnvVarDeclaration struct {
	IntegrationName string
	Key             string
	Sensitive       bool
	Description     string
}

type RuntimeConfigAliasDeclaration struct {
	IntegrationName string
	JSONKey         string
	EnvKey          string
	Description     string
}

type BotBootstrapDeclaration struct {
	IntegrationName string
	Index           int
	Config          extiface.BotBootstrapConfig
}

func RuntimeEnvDeclarations() []RuntimeEnvVarDeclaration {
	EnsureLoaded()
	out := []RuntimeEnvVarDeclaration{}
	for _, def := range List() {
		for _, envVar := range def.RuntimeEnvVars {
			out = append(out, RuntimeEnvVarDeclaration{
				IntegrationName: def.Name,
				Key:             envVar.Key,
				Sensitive:       envVar.Sensitive,
				Description:     envVar.Description,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IntegrationName == out[j].IntegrationName {
			return out[i].Key < out[j].Key
		}
		return out[i].IntegrationName < out[j].IntegrationName
	})
	return out
}

func RuntimeConfigAliasDeclarations() []RuntimeConfigAliasDeclaration {
	EnsureLoaded()
	out := []RuntimeConfigAliasDeclaration{}
	for _, def := range List() {
		for _, alias := range def.RuntimeConfigAliases {
			out = append(out, RuntimeConfigAliasDeclaration{
				IntegrationName: def.Name,
				JSONKey:         alias.JSONKey,
				EnvKey:          alias.EnvKey,
				Description:     alias.Description,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IntegrationName == out[j].IntegrationName {
			return out[i].JSONKey < out[j].JSONKey
		}
		return out[i].IntegrationName < out[j].IntegrationName
	})
	return out
}

func BotBootstrapDeclarations() []BotBootstrapDeclaration {
	EnsureLoaded()
	definitions := List()
	out := []BotBootstrapDeclaration{}
	for _, def := range definitions {
		for idx, cfg := range def.BotBootstrapConfigs {
			out = append(out, BotBootstrapDeclaration{
				IntegrationName: def.Name,
				Index:           idx,
				Config:          cloneBotBootstrapConfig(cfg),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IntegrationName == out[j].IntegrationName {
			return out[i].Index < out[j].Index
		}
		return out[i].IntegrationName < out[j].IntegrationName
	})
	return out
}

func cloneBotBootstrapConfig(input extiface.BotBootstrapConfig) extiface.BotBootstrapConfig {
	out := input
	out.AdditionalOwners = append([]string(nil), input.AdditionalOwners...)
	out.DefaultSharedConfig = cloneJSONMap(input.DefaultSharedConfig)
	return out
}

func cloneJSONMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		out := map[string]interface{}{}
		for k, v := range input {
			out[k] = v
		}
		return out
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
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

func AdditionalMigrations() []extiface.Migration {
	EnsureLoaded()
	migrations := []extiface.Migration{}
	for _, def := range List() {
		for _, migration := range def.Migrations {
			if migration.Run == nil {
				continue
			}
			migrations = append(migrations, migration)
		}
	}
	return migrations
}
