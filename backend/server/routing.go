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
	"backend/api/tools"
	"backend/api/user"
	"backend/api/websocket"
	"backend/scheduler"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
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
	matrixService *integrations.MatrixIntegrationService,
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
		SignalService: signalService,
		MatrixService: matrixService,
	}
	filesHandler := &files.FilesHandler{}
	toolsHandler := &tools.ToolsHandler{}
	mcpHandler := &tools.MCPHandler{}

	v1PrivateApis.HandleFunc("GET /chats/list", chatsHandler.List)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/messages/list", chatsHandler.ListMessages)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}", chatsHandler.GetChat)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/contact", contactsHandler.GetContactByChatUUID)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/messages/send", chatsHandler.MessageSend)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/signals/{signal}", chatsHandler.SignalSendMessage)
	v1PrivateApis.HandleFunc("POST /chats/create", chatsHandler.Create)

	// Tool execution endpoints (bot users only)
	v1PrivateApis.HandleFunc("POST /interactions/{chat_uuid}/tools/{tool_name}", toolsHandler.ExecuteTool)
	v1PrivateApis.HandleFunc("GET /interactions/{chat_uuid}/tools", toolsHandler.GetAvailableTools)
	v1PrivateApis.HandleFunc("POST /interactions/{chat_uuid}/tools/init", toolsHandler.StoreToolInitData)

	// MCP (Model Context Protocol) endpoints (bot users only)
	v1PrivateApis.HandleFunc("POST /interactions/{chat_uuid}/mcp", mcpHandler.HandleMCP)

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
	v1PrivateApis.HandleFunc("GET /federation/proxies/search", federationHandler.SearchProxies)
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

	// Matrix integration endpoints
	v1PrivateApis.HandleFunc("GET /integrations/matrix/{integration_id}/verification/status", integrationsHandler.GetMatrixVerificationStatus)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/verification/start", integrationsHandler.StartMatrixDeviceVerification)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/verification/{transaction_id}/confirm", integrationsHandler.ConfirmMatrixVerification)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/verification/{transaction_id}/cancel", integrationsHandler.CancelMatrixVerification)
	v1PrivateApis.HandleFunc("GET /integrations/matrix/{integration_id}/devices", integrationsHandler.ListMatrixDevices)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/devices/{device_id}/trust", integrationsHandler.TrustMatrixDevice)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/send", integrationsHandler.SendMatrixMessage)
	v1PrivateApis.HandleFunc("GET /integrations/matrix/{integration_id}/rooms", integrationsHandler.ListMatrixRooms)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/rooms/{room_id}/join", integrationsHandler.JoinMatrixRoom)
	v1PrivateApis.HandleFunc("GET /integrations/matrix/{integration_id}/whitelist", integrationsHandler.GetMatrixWhitelist)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/whitelist/add", integrationsHandler.AddToMatrixWhitelist)
	v1PrivateApis.HandleFunc("POST /integrations/matrix/{integration_id}/whitelist/remove", integrationsHandler.RemoveFromMatrixWhitelist)

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
		getDomainRoutingMiddleware(DB),
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
