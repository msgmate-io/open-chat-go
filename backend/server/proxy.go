package server

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"syscall"
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
	namespacedSessionCookie := mobileSessionCookieName(target)
	cacheCfg := resolveMobileProxyCacheConfig(target)

	baseDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		baseDirector(r)
		rewriteMobileProxyRequestCookies(r, namespacedSessionCookie)
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		rewriteMobileProxyResponseCookies(resp, namespacedSessionCookie)

		if !cacheCfg.Enabled || !mobileProxyRequestCacheable(resp.Request) {
			return nil
		}

		storeMobileProxyCacheResponse(resp, cacheCfg)
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if cacheCfg.Enabled && mobileProxyRequestCacheable(r) {
			if tryServeMobileProxyCachedResponse(w, r) {
				return
			}
		}
		defaultMobileProxyError(w, r, err)
	}

	return proxy
}

func rewriteMobileProxyResponseCookies(resp *http.Response, namespacedSessionCookie string) {
	setCookies := resp.Header.Values("Set-Cookie")
	if len(setCookies) == 0 {
		return
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
			if idx == 0 {
				trimmed = rewriteSessionCookieName(trimmed, namespacedSessionCookie)
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
		return
	}

	resp.Header.Del("Set-Cookie")
	for _, cookie := range rewritten {
		resp.Header.Add("Set-Cookie", cookie)
	}
}

func defaultMobileProxyError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json")

	if isMobileProxyOfflineRequest(r) || isMobileProxyOfflineError(err) {
		w.Header().Set("X-OpenChat-Mobile-Offline", "true")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"mobile_device_offline","message":"Device is offline and this data is not cached yet."}`))
		return
	}

	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(`{"error":"mobile_upstream_unavailable","message":"Mobile upstream unavailable."}`))
}

func isMobileProxyOfflineRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-OpenChat-Device-Online")), "false")
}

func isMobileProxyOfflineError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETDOWN) ||
		errors.Is(err, syscall.ENETRESET) {
		return true
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "network is unreachable") ||
		strings.Contains(message, "no route to host") ||
		strings.Contains(message, "host is unreachable") ||
		strings.Contains(message, "network is down") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "temporary failure in name resolution")
}

func mobileSessionCookieName(target *url.URL) string {
	key := strings.TrimSpace(target.String())
	h := sha1.Sum([]byte(strings.ToLower(key)))
	return "session_id_mobile_" + hex.EncodeToString(h[:])[:12]
}

func rewriteSessionCookieName(cookiePair string, namespacedSessionCookie string) string {
	idx := strings.Index(cookiePair, "=")
	if idx <= 0 {
		return cookiePair
	}
	name := strings.TrimSpace(cookiePair[:idx])
	if name != "session_id" {
		return cookiePair
	}
	return namespacedSessionCookie + cookiePair[idx:]
}

func rewriteMobileProxyRequestCookies(r *http.Request, namespacedSessionCookie string) {
	allCookies := r.Cookies()
	if len(allCookies) == 0 {
		return
	}

	rewritten := make([]*http.Cookie, 0, len(allCookies))
	var fallbackSessionCookie *http.Cookie
	var upstreamSessionCookie *http.Cookie

	for _, cookie := range allCookies {
		if cookie.Name == namespacedSessionCookie {
			copyCookie := *cookie
			copyCookie.Name = "session_id"
			upstreamSessionCookie = &copyCookie
			continue
		}

		if cookie.Name == "session_id" {
			if fallbackSessionCookie == nil {
				copyCookie := *cookie
				fallbackSessionCookie = &copyCookie
			}
			continue
		}

		rewritten = append(rewritten, cookie)
	}

	if upstreamSessionCookie != nil {
		rewritten = append(rewritten, upstreamSessionCookie)
	} else if fallbackSessionCookie != nil {
		rewritten = append(rewritten, fallbackSessionCookie)
	}

	r.Header.Del("Cookie")
	for _, cookie := range rewritten {
		r.AddCookie(cookie)
	}
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
