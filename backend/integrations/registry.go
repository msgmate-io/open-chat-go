package integrations

import (
	_ "backend/integrations/externalintegrations"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"
)

var (
	registryMu sync.RWMutex
	loaded     bool
	registry   = map[string]extiface.Definition{}
)

func EnsureLoaded() {
	registryMu.Lock()
	defer registryMu.Unlock()
	if loaded {
		return
	}
	registry = map[string]extiface.Definition{}
	for _, def := range extiface.List() {
		name := strings.ToLower(strings.TrimSpace(def.Name))
		if name == "" {
			continue
		}
		registry[name] = def
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
	_, ok := registry[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func Get(name string) (extiface.Definition, bool) {
	EnsureLoaded()
	registryMu.RLock()
	defer registryMu.RUnlock()
	def, ok := registry[strings.ToLower(strings.TrimSpace(name))]
	return def, ok
}

func Call(name string, functionName string, ctx context.Context, payload map[string]interface{}) (interface{}, error) {
	EnsureLoaded()
	registryMu.RLock()
	defer registryMu.RUnlock()
	def, ok := registry[strings.ToLower(strings.TrimSpace(name))]
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
