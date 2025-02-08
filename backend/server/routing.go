package server

import (
	"backend/api"
	"backend/api/admin"
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/files"
	"backend/api/integrations"
	"backend/api/metrics"
	"backend/api/reference"
	"backend/api/tls"
	"backend/api/user"
	"backend/api/websocket"
	"backend/database"
	"backend/scheduler"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed all:frontend routes.json swagger.json
var frontendFS embed.FS

func ProxyRequestHandler(proxy *httputil.ReverseProxy, url *url.URL, endpoint string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// fmt.Printf("[ TinyRP ] Request received at %s at %s\n", r.URL, time.Now().UTC())
		// Update the headers to allow for SSL redirection
		r.URL.Host = url.Host
		r.URL.Scheme = url.Scheme
		r.Header.Set("X-Forwarded-Host", r.Header.Get("Host"))
		r.Host = url.Host
		//trim reverseProxyRoutePrefix
		path := r.URL.Path
		r.URL.Path = strings.TrimLeft(path, endpoint)
		// Note that ServeHttp is non blocking and uses a go routine under the hood
		// fmt.Printf("[ TinyRP ] Redirecting request to %s at %s\n", r.URL, time.Now().UTC())
		proxy.ServeHTTP(w, r)
	}
}

func dbMiddleware(db *gorm.DB) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), "db", db)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func websocketMiddleware(ch *websocket.WebSocketHandler) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), "websocket", ch)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func frontendRedirectMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: for some paths do stuff
		next.ServeHTTP(w, r)
	})
}

// isLikelyMainServerDomain checks if a domain is likely the main server domain
// vs a subdomain or different domain that should be a proxy
func isLikelyMainServerDomain(host string) bool {
	// Common patterns that suggest this is the main server domain:
	// 1. No subdomain (e.g., "example.com" vs "sub.example.com")
	// 2. Common TLDs
	// 3. Not obviously a subdomain pattern

	// Split by dots to analyze the domain structure
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return false // Invalid domain
	}

	// If it has more than 2 parts, it's likely a subdomain (e.g., "sub.example.com")
	if len(parts) > 2 {
		return false
	}

	// Check if the TLD is a common one (this is a heuristic)
	commonTLDs := map[string]bool{
		"com": true, "org": true, "net": true, "io": true, "co": true,
		"uk": true, "de": true, "fr": true, "it": true, "es": true,
		"ca": true, "au": true, "jp": true, "cn": true, "in": true,
		"ru": true, "br": true, "mx": true, "nl": true, "se": true,
		"no": true, "dk": true, "fi": true, "pl": true, "cz": true,
	}

	tld := parts[len(parts)-1]
	if commonTLDs[tld] {
		return true
	}

	// If it's a 2-part domain with a common TLD, it's likely the main domain
	return true
}

// domainRoutingMiddleware handles domain-based routing for domain proxies
func domainRoutingMiddleware(DB *gorm.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract domain from Host header
			host := r.Host
			if i := strings.Index(host, ":"); i != -1 {
				host = host[:i] // Remove port if present
			}

			// Skip domain routing for localhost and IP addresses
			if host == "localhost" || host == "127.0.0.1" || net.ParseIP(host) != nil {
				next.ServeHTTP(w, r)
				return
			}

			// Check if this is a domain proxy
			var proxy database.Proxy
			err := DB.Where("kind = ? AND active = ? AND traffic_origin = ?", "domain", true, host).First(&proxy).Error
			if err == nil {
				// This is a domain proxy request
				// TrafficTarget contains "cert_prefix:backend_port" format
				targetParts := strings.Split(proxy.TrafficTarget, ":")
				if len(targetParts) >= 2 {
					backendPort := targetParts[1]

					// Create a reverse proxy to the backend port
					backendURL := fmt.Sprintf("http://localhost:%s", backendPort)
					targetURL, err := url.Parse(backendURL)
					if err == nil {
						proxy := httputil.NewSingleHostReverseProxy(targetURL)

						// Set headers for proper proxying
						r.Header.Set("X-Forwarded-Host", r.Host)
						r.Header.Set("X-Forwarded-Proto", "https")

						log.Printf("Domain proxy: routing %s to %s", host, backendURL)
						proxy.ServeHTTP(w, r)
						return
					} else {
						log.Printf("Error: Failed to parse backend URL %s for domain %s: %v", backendURL, host, err)
					}
				} else {
					log.Printf("Error: Invalid TrafficTarget format for domain %s: %s", host, proxy.TrafficTarget)
				}
			} else if err != gorm.ErrRecordNotFound {
				// Log database errors but don't fail the request
				log.Printf("Error: Database error while checking domain proxy for %s: %v", host, err)
			} else {
				// No domain proxy found for this domain
				// Check if this looks like a domain that should be a proxy vs the main server domain
				// We'll be more conservative and only show the error for domains that clearly look like proxies

				// If this is likely the main server domain (no subdomain, common TLD), fall through to normal routing
				// This prevents the main domain from showing "domain proxy not configured" errors
				if isLikelyMainServerDomain(host) {
					log.Printf("Domain %s appears to be the main server domain, falling through to normal routing", host)
					next.ServeHTTP(w, r)
					return
				}

				// This looks like a domain that should be a proxy but isn't configured
				// Return a proper error message instead of falling through to normal routing
				// This prevents permanent redirects that get cached by browsers
				log.Printf("No domain proxy configured for domain: %s", host)

				// Check if this is an HTML request (browser) or text request (curl)
				accept := r.Header.Get("Accept")
				if strings.Contains(accept, "text/html") {
					// Return HTML for browsers
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(http.StatusNotFound)

					html := fmt.Sprintf(`
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Domain Proxy Not Configured</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
            margin: 0;
            padding: 0;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .container {
            background: white;
            border-radius: 12px;
            padding: 2rem;
            box-shadow: 0 20px 40px rgba(0,0,0,0.1);
            text-align: center;
            max-width: 500px;
            margin: 1rem;
        }
        .icon {
            font-size: 4rem;
            margin-bottom: 1rem;
        }
        h1 {
            color: #333;
            margin-bottom: 1rem;
            font-size: 1.5rem;
        }
        p {
            color: #666;
            line-height: 1.6;
            margin-bottom: 1.5rem;
        }
        .domain {
            font-family: monospace;
            background: #f5f5f5;
            padding: 0.5rem;
            border-radius: 4px;
            color: #333;
            font-weight: bold;
        }
        .info {
            background: #e3f2fd;
            border-left: 4px solid #2196f3;
            padding: 1rem;
            margin: 1rem 0;
            text-align: left;
        }
        .info h3 {
            margin: 0 0 0.5rem 0;
            color: #1976d2;
        }
        .info ul {
            margin: 0;
            padding-left: 1.5rem;
        }
        .info li {
            margin: 0.25rem 0;
            color: #555;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="icon">🔧</div>
        <h1>Domain Proxy Not Configured</h1>
        <p>No proxy has been set up for the domain:</p>
        <div class="domain">%s</div>
        
        <div class="info">
            <h3>What does this mean?</h3>
            <ul>
                <li>This domain is not configured as a proxy target</li>
                <li>No backend service is mapped to this domain</li>
                <li>The domain proxy needs to be created in the admin panel</li>
            </ul>
        </div>
        
        <p><strong>Status:</strong> 404 Not Found</p>
        <p><em>This is a temporary response and will not be cached by browsers.</em></p>
    </div>
</body>
</html>`, host)

					w.Write([]byte(html))
					return
				} else {
					// Return plain text for curl and other non-HTML clients
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(http.StatusNotFound)

					text := fmt.Sprintf(`No domain proxy configured for domain: %s

This domain is not configured as a proxy target. No backend service is mapped to this domain.

Status: 404 Not Found
This is a temporary response and will not be cached by browsers.

To set up a domain proxy, create one in the admin panel.`, host)

					w.Write([]byte(text))
					return
				}
			}

			// Not a domain proxy or error occurred, continue with normal routing
			next.ServeHTTP(w, r)
		})
	}
}

func getFrontendRoutes() ([]string, error) {
	content, err := frontendFS.ReadFile("routes.json")
	if err != nil {
		return nil, fmt.Errorf("error reading routes.json: %w", err)
	}

	var routes []string
	if err := json.Unmarshal(content, &routes); err != nil {
		return nil, fmt.Errorf("error parsing routes.json: %w", err)
	}

	return routes, nil
}

func ServeFrontendRoute(route string, pathEnding string) func(http.ResponseWriter, *http.Request) {
	fsys, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	fs := http.FileServer(http.FS(fsys))

	return func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "text/html") {
			// serve all other assets normally
			fs.ServeHTTP(w, r)
			return
		}

		// e.g.: route = "/star-wars/{id}"
		// the html file we have to server is at 'route/index.html'
		// but we also need to match all possible '{}' paths in the route
		// e.g.: for /star-wars/{id} we need to replace {id} in the html file with r.PathValue("id")
		// and then serve the html file
		regMatch := regexp.MustCompile(`{(.*?)}`)
		pathValues := make(map[string]string)
		matches := regMatch.FindAllStringSubmatch(route, -1)
		for _, match := range matches {
			if val := r.PathValue(match[1]); val != "" {
				pathValues[match[1]] = val
			} else {
				log.Printf("Warning: No value found for path parameter %s", match[1])
				pathValues[match[1]] = match[1]
			}
		}

		// Remove the leading slash from the route when accessing the filesystem
		cleanRoute := strings.TrimPrefix(route, "/")
		staticFile := fmt.Sprintf("%s%s", cleanRoute, pathEnding)
		indexFile, err := fsys.Open(staticFile)
		if err != nil {
			log.Printf("Error opening index.html for route %s: %v", cleanRoute, err)
			http.Error(w, "Page Not Found", http.StatusNotFound)
			return
		}
		defer indexFile.Close()

		content, err := io.ReadAll(indexFile)
		if err != nil {
			log.Println(err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Replace all the path values in the html file
		for key, value := range pathValues {
			content = bytes.Replace(content, []byte(fmt.Sprintf("{%s}", key)), []byte(value), -1)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(content))
	}
}

func BackendRouting(
	DB *gorm.DB,
	federationHandler api.FederationHandlerInterface,
	schedulerService *scheduler.SchedulerService,
	signalService *integrations.SignalIntegrationService,
	debug bool,
	frontendProxy string,
	cookieDomain string,
) (*http.ServeMux, *websocket.WebSocketHandler) {
	mux := http.NewServeMux()
	v1PrivateApis := http.NewServeMux()
	websocketMux := http.NewServeMux()
	terminalMux := http.NewServeMux()

	userHandler := &user.UserHandler{
		DB:           DB,
		CookieDomain: cookieDomain,
	}
	chatsHandler := &chats.ChatsHandler{}
	contactsHandler := &contacts.ContactsHander{}
	metricsHandler := &metrics.MetricsHandler{}
	websocketHandler := websocket.NewWebSocketHandler()
	integrationsHandler := &integrations.IntegrationsHandler{
		SchedulerService: schedulerService,
		SignalService:    signalService,
	}
	filesHandler := &files.FilesHandler{}

	v1PrivateApis.HandleFunc("GET /chats/list", chatsHandler.List)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/messages/list", chatsHandler.ListMessages)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}", chatsHandler.GetChat)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/contact", contactsHandler.GetContactByChatUUID)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/messages/send", chatsHandler.MessageSend)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/signals/{signal}", chatsHandler.SignalSendMessage)
	v1PrivateApis.HandleFunc("POST /chats/create", chatsHandler.Create)

	v1PrivateApis.HandleFunc("POST /contacts/add", contactsHandler.Add)
	v1PrivateApis.HandleFunc("GET  /contacts/list", contactsHandler.List)
	v1PrivateApis.HandleFunc("GET /contacts/{contact_token}", contactsHandler.GetContactByToken)

	v1PrivateApis.HandleFunc("GET /user/self", userHandler.Self)
	v1PrivateApis.HandleFunc("POST /user/2fa/setup", userHandler.SetupTwoFactor)
	v1PrivateApis.HandleFunc("POST /user/2fa/confirm", userHandler.ConfirmTwoFactor)
	v1PrivateApis.HandleFunc("POST /user/2fa/disable", userHandler.DisableTwoFactor)
	v1PrivateApis.HandleFunc("POST /user/2fa/recovery-codes", userHandler.GenerateNewRecoveryCodes)
	v1PrivateApis.HandleFunc("GET /federation/identity", federationHandler.Identity)
	v1PrivateApis.HandleFunc("GET /federation/bootstrap", federationHandler.Bootstrap)
	v1PrivateApis.HandleFunc("POST /federation/networks/{network_name}/request-relay-reservation", federationHandler.NetworkRequestRelayReservation)
	v1PrivateApis.HandleFunc("POST /federation/networks/{network_name}/forward-request", federationHandler.NetworkForwardRelayReservation)
	v1PrivateApis.HandleFunc("GET /federation/networks/list", federationHandler.ListNetworks)
	v1PrivateApis.HandleFunc("POST /federation/networks/create", federationHandler.NetworkCreate)
	v1PrivateApis.HandleFunc("DELETE /federation/networks/{network_name}", federationHandler.DeleteNetwork)
	v1PrivateApis.HandleFunc("DELETE /federation/networks/{network_name}/nodes/{node_peer_id}", federationHandler.DeleteNodeFromNetwork)
	v1PrivateApis.HandleFunc("POST /federation/networks/{network_name}/nodes/{node_peer_id}/restore", federationHandler.RestoreNodeFromNetwork)
	v1PrivateApis.HandleFunc("POST /federation/nodes/register", federationHandler.RegisterNode)
	v1PrivateApis.HandleFunc("GET /federation/nodes/list", federationHandler.ListNodes)
	v1PrivateApis.HandleFunc("GET /federation/nodes/{node_uuid}", federationHandler.GetNode)
	v1PrivateApis.HandleFunc("GET /federation/nodes/whitelisted", federationHandler.WhitelistedPeers)
	v1PrivateApis.HandleFunc("POST /federation/nodes/{node_uuid}/request", federationHandler.RequestNode)
	v1PrivateApis.HandleFunc("POST /federation/nodes/peer/{peer_id}/request", federationHandler.RequestNodeByPeerId)
	v1PrivateApis.HandleFunc("POST /federation/nodes/proxy", federationHandler.CreateAndStartProxy)
	v1PrivateApis.HandleFunc("GET /federation/proxies/list", federationHandler.ListProxies)
	v1PrivateApis.HandleFunc("DELETE /federation/proxies/{id}", federationHandler.DeleteProxy)
	v1PrivateApis.HandleFunc("POST /federation/proxies/reload", federationHandler.ReloadDomainProxies)
	v1PrivateApis.HandleFunc("DELETE /tls/keys/{key_name}", tls.DeleteKey)
	v1PrivateApis.HandleFunc("GET /tls/keys", tls.ListKeys)
	v1PrivateApis.HandleFunc("GET /keys/names", tls.ListKeyNames)
	v1PrivateApis.HandleFunc("GET /keys/{key_name}/get", tls.RetrieveKey)
	v1PrivateApis.HandleFunc("POST /keys/create", tls.CreateKey)
	v1PrivateApis.HandleFunc("POST /tls/acme/solve", tls.SolveACMEChallengeHandler)
	v1PrivateApis.HandleFunc("POST /tls/acme/renew", tls.RenewTLSCertificateHandler)

	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}", admin.GetTableInfo)
	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}/data", admin.GetTableDataPaginated)
	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}/{id}", admin.GetTableItemById)
	v1PrivateApis.HandleFunc("DELETE /admin/table/{table_name}/{id}", admin.DeleteTableItemById)
	v1PrivateApis.HandleFunc("DELETE /admin/delete_all_entries/{table_name}", admin.DeleteAllEntries)
	v1PrivateApis.HandleFunc("GET /admin/tables", admin.GetAllTables)
	v1PrivateApis.HandleFunc("GET /admin/users", admin.GetUsersWithDetails)

	v1PrivateApis.HandleFunc("GET /metrics", metricsHandler.Metrics)

	v1PrivateApis.HandleFunc("POST /bin/upload", federationHandler.UploadBinary)
	v1PrivateApis.HandleFunc("POST /bin/request-self-update", federationHandler.RequestSelfUpdate)
	v1PrivateApis.HandleFunc("POST /federation/networks/addnode", federationHandler.RegisterNode)
	v1PrivateApis.HandleFunc("GET /federation/networks/sync/{network_name}/get", federationHandler.SyncGet)

	v1PrivateApis.HandleFunc("GET /integrations/list", integrationsHandler.List)
	v1PrivateApis.HandleFunc("GET /integrations/{id}", integrationsHandler.Get)
	v1PrivateApis.HandleFunc("POST /integrations/{id}/toggle-active", integrationsHandler.ToggleActive)
	v1PrivateApis.HandleFunc("DELETE /integrations/{id}", integrationsHandler.Delete)
	v1PrivateApis.HandleFunc("GET /integrations/{integration_type}/{integration_alias}/status", integrationsHandler.GetIntegrationStatus)
	v1PrivateApis.HandleFunc("POST /integrations/create", integrationsHandler.Create)
	v1PrivateApis.HandleFunc("POST /integrations/signal/install", integrationsHandler.InstallSignalRest)
	v1PrivateApis.HandleFunc("POST /integrations/signal/uninstall", integrationsHandler.UninstallSignalRest)

	v1PrivateApis.HandleFunc("GET /integrations/signal/{alias}/whitelist", integrationsHandler.GetSignalWhitelist)
	v1PrivateApis.HandleFunc("POST /integrations/signal/{alias}/whitelist/add", integrationsHandler.AddToSignalWhitelist)
	v1PrivateApis.HandleFunc("POST /integrations/signal/{alias}/whitelist/remove", integrationsHandler.RemoveFromSignalWhitelist)

	// Add schedulerService to the admin handler
	scheduledTasksHandler := &admin.ScheduledTasksHandler{
		SchedulerService: schedulerService,
	}

	// Add new routes for scheduled tasks
	v1PrivateApis.HandleFunc("GET /admin/tasks", scheduledTasksHandler.ListTasks)
	v1PrivateApis.HandleFunc("POST /admin/tasks/{task_name}/run", scheduledTasksHandler.RunTask)
	v1PrivateApis.HandleFunc("POST /admin/tasks", scheduledTasksHandler.AddTask)
	v1PrivateApis.HandleFunc("DELETE /admin/tasks/{task_name}", scheduledTasksHandler.RemoveTask)

	v1PrivateApis.HandleFunc("POST /files/upload", filesHandler.UploadFile)
	v1PrivateApis.HandleFunc("GET /files/{file_id}", filesHandler.GetFile)
	v1PrivateApis.HandleFunc("GET /files/{file_id}/info", filesHandler.GetFileInfo)
	v1PrivateApis.HandleFunc("GET /files/{file_id}/data", filesHandler.GetFileData)
	v1PrivateApis.HandleFunc("DELETE /files/{file_id}", filesHandler.DeleteFile)

	providerMiddlewares := CreateStack(
		domainRoutingMiddleware(DB),
		dbMiddleware(DB),
		websocketMiddleware(websocketHandler),
	)

	websocketMux.HandleFunc("/connect", websocketHandler.Connect)
	terminalMux.HandleFunc("/terminal", federationHandler.WebTerminalHandler) // '/federation/terminal'
	mux.Handle("/ws/", http.StripPrefix("/ws", providerMiddlewares(AuthMiddleware(websocketMux))))
	mux.Handle("/federation/", http.StripPrefix("/federation", providerMiddlewares(terminalMux)))
	mux.Handle("POST /api/v1/user/login", providerMiddlewares(http.HandlerFunc(userHandler.Login)))
	mux.Handle("POST /api/v1/user/logout", providerMiddlewares(http.HandlerFunc(userHandler.Logout)))

	mux.Handle("GET /api/v1/bin/download", providerMiddlewares(http.HandlerFunc(federationHandler.DownloadBinary)))
	mux.Handle("GET /api/v1/bin/setup", providerMiddlewares(http.HandlerFunc(federationHandler.GetHiveSetupScript)))
	mux.Handle("POST /api/v1/user/register", providerMiddlewares(http.HandlerFunc(userHandler.Register)))
	mux.Handle("POST /api/v1/federation/networks/login", providerMiddlewares(http.HandlerFunc(userHandler.NetworkUserLogin)))

	// Add ACME challenge handler for Let's Encrypt verification
	mux.HandleFunc("/.well-known/acme-challenge/", tls.ACMEChallengeHandler)

	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", providerMiddlewares(Logging(AuthMiddleware(v1PrivateApis)))))

	// Create swagger reference handler with embedded content
	swaggerContent, err := frontendFS.ReadFile("swagger.json")
	if err != nil {
		log.Printf("Warning: Failed to read embedded swagger.json: %v", err)
		// Fallback to file-based handler
		mux.HandleFunc("/reference", reference.ScalarReference)
		mux.HandleFunc("/api/reference", reference.ScalarReference)
	} else {
		// Use embedded content
		swaggerHandler := reference.ScalarReferenceWithContent(string(swaggerContent))
		mux.HandleFunc("/reference", swaggerHandler)
		mux.HandleFunc("/api/reference", swaggerHandler)
	}

	mux.HandleFunc("/api/version", reference.VersionHandler)

	if frontendProxy == "" {
		routes, err := getFrontendRoutes()
		if err != nil {
			log.Printf("Warning: Failed to load routes from routes.json: %v", err)
			routes = []string{}
		}

		for _, route := range routes {
			fmt.Printf("Serving route: %s\n", route)
			mux.Handle(route, providerMiddlewares(FrontendAuthMiddleware(http.HandlerFunc(ServeFrontendRoute(route, "/index.html")))))
		}
		mux.Handle("/", providerMiddlewares(FrontendAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				ServeFrontendRoute("/", "index.html")(w, r)
			} else {
				ServeFrontendRoute("/404", ".html")(w, r)
			}
		}))))
	} else {
		targetURL, err := url.Parse(frontendProxy)
		if err != nil {
			log.Fatal(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		mux.Handle("/", providerMiddlewares(FrontendAuthMiddleware(http.HandlerFunc(
			ProxyRequestHandler(proxy, targetURL, "/"),
		))))
	}

	return mux, websocketHandler
}
