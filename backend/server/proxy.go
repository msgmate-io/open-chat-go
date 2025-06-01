package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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

func newMobileAPIWSReverseProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := newReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		setCookies := resp.Header.Values("Set-Cookie")
		if len(setCookies) == 0 {
			return nil
		}

		rewritten := make([]string, 0, len(setCookies))
		for _, rawCookie := range setCookies {
			parts := strings.Split(rawCookie, ";")
			filtered := make([]string, 0, len(parts))
			for idx, part := range parts {
				trimmed := strings.TrimSpace(part)
				if trimmed == "" {
					continue
				}
				if idx > 0 {
					lower := strings.ToLower(trimmed)
					if lower == "secure" || strings.HasPrefix(lower, "domain=") {
						continue
					}
				}
				filtered = append(filtered, trimmed)
			}
			if len(filtered) > 0 {
				rewritten = append(rewritten, strings.Join(filtered, "; "))
			}
		}

		if len(rewritten) == 0 {
			resp.Header.Del("Set-Cookie")
			return nil
		}

		resp.Header.Del("Set-Cookie")
		for _, cookie := range rewritten {
			resp.Header.Add("Set-Cookie", cookie)
		}
		return nil
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
