package server

import (
	"backend/api/admin"
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/federation"
	"backend/api/metrics"
	"backend/api/reference"
	"backend/api/tls"
	"backend/api/user"
	"backend/api/websocket"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed all:frontend routes.json
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
	federationHandler *federation.FederationHandler,
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
	v1PrivateApis.HandleFunc("GET /federation/identity", federationHandler.Identity)
	v1PrivateApis.HandleFunc("POST /federation/networks/{network_name}/request-relay-reservation", federationHandler.NetworkRequestRelayReservation)
	v1PrivateApis.HandleFunc("POST /federation/networks/{network_name}/forward-request", federationHandler.NetworkForwardRelayReservation)
	v1PrivateApis.HandleFunc("POST /federation/nodes/register", federationHandler.RegisterNode)
	v1PrivateApis.HandleFunc("GET /federation/nodes/list", federationHandler.ListNodes)
	v1PrivateApis.HandleFunc("GET /federation/nodes/whitelisted", federationHandler.WhitelistedPeers)
	v1PrivateApis.HandleFunc("POST /federation/nodes/{node_uuid}/request", federationHandler.RequestNode)
	v1PrivateApis.HandleFunc("POST /federation/nodes/peer/{peer_id}/request", federationHandler.RequestNodeByPeerId)
	v1PrivateApis.HandleFunc("POST /federation/nodes/proxy", federationHandler.CreateAndStartProxy)
	v1PrivateApis.HandleFunc("GET /federation/proxies/list", federationHandler.ListProxies)
	v1PrivateApis.HandleFunc("DELETE /tls/keys/{key_name}", tls.DeleteKey)
	v1PrivateApis.HandleFunc("GET /tls/keys", tls.ListKeys)
	v1PrivateApis.HandleFunc("GET /keys/names", tls.ListKeyNames)
	v1PrivateApis.HandleFunc("GET /keys/{key_name}/get", tls.RetrieveKey)
	v1PrivateApis.HandleFunc("POST /keys/create", tls.CreateKey)
	v1PrivateApis.HandleFunc("POST /tls/acme/solve", tls.SolveACMEChallengeHandler)

	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}", admin.GetTableInfo)
	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}/data", admin.GetTableDataPaginated)
	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}/{id}", admin.GetTableItemById)
	v1PrivateApis.HandleFunc("DELETE /admin/table/{table_name}/{id}", admin.DeleteTableItemById)
	v1PrivateApis.HandleFunc("GET /admin/tables", admin.GetAllTables)

	v1PrivateApis.HandleFunc("GET /metrics", metricsHandler.Metrics)

	v1PrivateApis.HandleFunc("POST /bin/upload", federationHandler.UploadBinary)
	v1PrivateApis.HandleFunc("POST /bin/request-self-update", federationHandler.RequestSelfUpdate)
	v1PrivateApis.HandleFunc("POST /federation/networks/addnode", federationHandler.RegisterNode)
	v1PrivateApis.HandleFunc("GET /federation/networks/sync/{network_name}/get", federationHandler.SyncGet)

	providerMiddlewares := CreateStack(
		dbMiddleware(DB),
		websocketMiddleware(websocketHandler),
	)

	websocketMux.HandleFunc("/connect", websocketHandler.Connect)
	terminalMux.HandleFunc("/terminal", federationHandler.WebTerminalHandler)
	mux.Handle("/ws/", http.StripPrefix("/ws", providerMiddlewares(AuthMiddleware(websocketMux))))
	mux.Handle("/federation/", http.StripPrefix("/federation", providerMiddlewares(terminalMux)))
	mux.Handle("POST /api/v1/user/login", providerMiddlewares(http.HandlerFunc(userHandler.Login)))
	mux.Handle("POST /api/v1/user/logout", providerMiddlewares(http.HandlerFunc(userHandler.Logout)))

	mux.Handle("GET /api/v1/bin/download", providerMiddlewares(http.HandlerFunc(federationHandler.DownloadBinary)))
	mux.Handle("GET /api/v1/bin/setup", providerMiddlewares(http.HandlerFunc(federationHandler.GetHiveSetupScript)))
	mux.Handle("POST /api/v1/user/register", providerMiddlewares(http.HandlerFunc(userHandler.Register)))
	mux.Handle("POST /api/v1/federation/networks/login", providerMiddlewares(http.HandlerFunc(userHandler.NetworkUserLogin)))
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", providerMiddlewares(Logging(AuthMiddleware(v1PrivateApis)))))
	mux.HandleFunc("/reference", reference.ScalarReference)
	mux.HandleFunc("/api/reference", reference.ScalarReference)

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
