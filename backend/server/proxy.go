package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// FrontendProxy describes a reverse-proxy target that owns one or more path
// prefixes. It lets the backend front one or more dev servers behind a single origin.
type FrontendProxy struct {
	Name     string
	Target   string   // upstream base URL, e.g. http://frontend:3000
	Prefixes []string // path prefixes this proxy owns ("/" is the catch-all)
	Public   bool     // skip the login redirect for public proxy paths
}

func newReverseProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		baseDirector(r)
		r.Header.Set("X-Forwarded-Host", r.Host)
		r.Host = target.Host
	}
	return proxy
}

// registerFrontendProxies wires each proxy's prefixes onto the mux. Paths are
// forwarded untouched so upstream dev servers receive the original URL.
func registerFrontendProxies(mux *http.ServeMux, proxies []FrontendProxy, commonMiddlewares Middleware) error {
	for _, p := range proxies {
		if p.Target == "" {
			continue
		}
		target, err := url.Parse(p.Target)
		if err != nil {
			return fmt.Errorf("invalid proxy target %q for %q: %w", p.Target, p.Name, err)
		}

		var handler http.Handler = newReverseProxy(target)
		if !p.Public {
			handler = FrontendAuthMiddleware(handler)
		}
		wrapped := commonMiddlewares(handler)

		for _, prefix := range p.Prefixes {
			fmt.Printf("Proxying %s -> %s (%s)\n", prefix, p.Target, p.Name)
			mux.Handle(prefix, wrapped)
		}
	}
	return nil
}
