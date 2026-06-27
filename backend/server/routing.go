package server

import (
	"backend/api/admin"
	"backend/api/bots"
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/files"
	"backend/api/metrics"
	"backend/api/models"
	"backend/api/reference"
	"backend/api/tools"
	"backend/api/user"
	"backend/api/websocket"
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
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hibiken/asynq"
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

func queueMiddleware(queueClient *asynq.Client, queueInspector *asynq.Inspector) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), "asynq_client", queueClient)
			ctx = context.WithValue(ctx, "asynq_inspector", queueInspector)
			next.ServeHTTP(w, r.WithContext(ctx))
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
	fileServer := http.FileServer(http.FS(fsys))

	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(pathEnding, ".json") && strings.HasSuffix(r.URL.Path, ".pageContext.json") {
			pathValues := pathValuesFromRoute(route, r)
			for key, value := range pathValues {
				pathValues[key] = trimPageContextSuffix(value)
			}
			serveFrontendRouteFile(route, "/index.pageContext.json", pathValues, w, r)
			return
		}

		if !strings.HasSuffix(pathEnding, ".json") {
			accept := r.Header.Get("Accept")
			if !strings.Contains(accept, "text/html") {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		pathValues := pathValuesFromRoute(route, r)
		serveFrontendRouteFile(route, pathEnding, pathValues, w, r)
	}
}

func pathValuesFromRoute(route string, r *http.Request) map[string]string {
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
	return pathValues
}

func trimPageContextSuffix(value string) string {
	value = strings.TrimSuffix(value, "/index.pageContext.json")
	value = strings.TrimSuffix(value, ".pageContext.json")
	if strings.HasSuffix(value, "/") && value != "/" {
		value = strings.TrimSuffix(value, "/")
	}
	return value
}

func serveFrontendRouteFile(route string, pathEnding string, pathValues map[string]string, w http.ResponseWriter, r *http.Request) bool {
	fsys, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return false
	}

	cleanRoute := strings.TrimPrefix(route, "/")
	staticFile := fmt.Sprintf("%s%s", cleanRoute, pathEnding)
	indexFile, err := fsys.Open(staticFile)
	if err != nil {
		log.Printf("Error opening %s for route %s: %v", pathEnding, cleanRoute, err)
		http.Error(w, "Page Not Found", http.StatusNotFound)
		return false
	}
	defer indexFile.Close()

	content, err := io.ReadAll(indexFile)
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return false
	}

	for key, value := range pathValues {
		content = bytes.Replace(content, []byte(fmt.Sprintf("{%s}", key)), []byte(value), -1)
	}

	contentType := "text/html; charset=utf-8"
	if strings.HasSuffix(pathEnding, ".json") {
		contentType = "application/json; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(pathEnding), time.Time{}, bytes.NewReader(content))
	return true
}

func tryServeFrontendPageContext(routes []string, w http.ResponseWriter, r *http.Request) bool {
	requestPath := r.URL.Path
	if !strings.HasSuffix(requestPath, ".pageContext.json") {
		return false
	}

	logicalPath := strings.TrimSuffix(requestPath, ".pageContext.json")
	logicalPath = strings.TrimSuffix(logicalPath, "/index")
	if strings.HasSuffix(logicalPath, "/") && logicalPath != "/" {
		logicalPath = strings.TrimSuffix(logicalPath, "/")
	}
	if logicalPath == "" {
		logicalPath = "/"
	}

	type routeCandidate struct {
		route      string
		pathValues map[string]string
	}

	best := routeCandidate{}
	found := false
	for _, route := range routes {
		pathValues, ok := matchRequestPathToRoute(route, logicalPath)
		if !ok {
			continue
		}
		if !found || isRouteMoreSpecific(route, best.route) {
			best = routeCandidate{route: route, pathValues: pathValues}
			found = true
		}
	}

	if !found {
		return false
	}

	return serveFrontendRouteFile(best.route, "/index.pageContext.json", best.pathValues, w, r)
}

func matchRequestPathToRoute(routePattern string, requestPath string) (map[string]string, bool) {
	patternSegments := routeSegments(routePattern)
	pathSegments := routeSegments(requestPath)
	if len(patternSegments) != len(pathSegments) {
		return nil, false
	}

	pathValues := map[string]string{}
	for i := range patternSegments {
		routeSegment := patternSegments[i]
		pathSegment := pathSegments[i]

		if strings.HasPrefix(routeSegment, "{") && strings.HasSuffix(routeSegment, "}") {
			param := strings.TrimSuffix(strings.TrimPrefix(routeSegment, "{"), "}")
			if param != "" {
				pathValues[param] = pathSegment
			}
			continue
		}

		if routeSegment != pathSegment {
			return nil, false
		}
	}

	return pathValues, true
}

func routeSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "/")
}

func isRouteMoreSpecific(a string, b string) bool {
	aSeg := routeSegments(a)
	bSeg := routeSegments(b)

	aDynamic := 0
	for _, segment := range aSeg {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			aDynamic++
		}
	}
	bDynamic := 0
	for _, segment := range bSeg {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			bDynamic++
		}
	}

	if aDynamic != bDynamic {
		return aDynamic < bDynamic
	}

	if len(aSeg) != len(bSeg) {
		return len(aSeg) > len(bSeg)
	}

	return a < b
}

func tryServeFrontendHTMLRoute(routes []string, w http.ResponseWriter, r *http.Request) bool {
	logicalPath := r.URL.Path
	if logicalPath == "" {
		logicalPath = "/"
	}
	if strings.HasSuffix(logicalPath, "/") && logicalPath != "/" {
		logicalPath = strings.TrimSuffix(logicalPath, "/")
	}

	type routeCandidate struct {
		route      string
		pathValues map[string]string
	}

	best := routeCandidate{}
	found := false
	for _, route := range routes {
		pathValues, ok := matchRequestPathToRoute(route, logicalPath)
		if !ok {
			continue
		}
		if !found || isRouteMoreSpecific(route, best.route) {
			best = routeCandidate{route: route, pathValues: pathValues}
			found = true
		}
	}

	if !found {
		return false
	}

	return serveFrontendRouteFile(best.route, "/index.html", best.pathValues, w, r)
}

func BackendRouting(
	DB *gorm.DB,
	queueClient *asynq.Client,
	queueInspector *asynq.Inspector,
	asynqUIHandler http.Handler,
	debug bool,
	frontendProxy string,
	sessionCookieDomain string,
) (*http.ServeMux, *websocket.WebSocketHandler) {
	mux := http.NewServeMux()
	v1PrivateApis := http.NewServeMux()
	websocketMux := http.NewServeMux()

	userHandler := &user.UserHandler{
		DB:           DB,
		CookieDomain: sessionCookieDomain,
	}
	chatsHandler := &chats.ChatsHandler{}
	contactsHandler := &contacts.ContactsHander{}
	metricsHandler := &metrics.MetricsHandler{}
	websocketHandler := websocket.NewWebSocketHandler()
	filesHandler := &files.FilesHandler{}
	toolsHandler := &tools.ToolsHandler{}
	mcpHandler := &tools.MCPHandler{}
	modelsHandler := &models.ModelsHandler{}
	botsHandler := &bots.BotsHandler{}

	v1PrivateApis.HandleFunc("GET /chats/list", chatsHandler.List)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/messages/list", chatsHandler.ListMessages)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}", chatsHandler.GetChat)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/contact", contactsHandler.GetContactByChatUUID)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/messages/send", chatsHandler.MessageSend)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/messages/{message_uuid}/rerun", chatsHandler.RerunMessage)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/messages/{message_uuid}/confirm-actions/{action_id}/execute", toolsHandler.ExecuteConfirmableAction)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/signals/{signal}", chatsHandler.SignalSendMessage)
	v1PrivateApis.HandleFunc("POST /chats/create", chatsHandler.Create)
	v1PrivateApis.HandleFunc("POST /bots", botsHandler.Create)
	v1PrivateApis.HandleFunc("GET /bots/list", botsHandler.List)
	v1PrivateApis.HandleFunc("GET /bots/{identifier}", botsHandler.Get)
	v1PrivateApis.HandleFunc("PATCH /bots/{identifier}", botsHandler.Update)
	v1PrivateApis.HandleFunc("PUT /bots/{identifier}/config", botsHandler.SaveConfig)
	v1PrivateApis.HandleFunc("DELETE /bots/{identifier}", botsHandler.Delete)
	v1PrivateApis.HandleFunc("POST /bots/{identifier}/interactions", botsHandler.CreateInteraction)

	// Tool execution endpoints (bot users only)
	v1PrivateApis.HandleFunc("POST /interactions/{chat_uuid}/tools/{tool_name}", toolsHandler.ExecuteTool)
	v1PrivateApis.HandleFunc("POST /interactions/{chat_uuid}/tools/{tool_name}/enqueue", toolsHandler.EnqueueTool)
	v1PrivateApis.HandleFunc("GET /interactions/{chat_uuid}/tools", toolsHandler.GetAvailableTools)
	v1PrivateApis.HandleFunc("POST /interactions/{chat_uuid}/tools/init", toolsHandler.StoreToolInitData)

	// MCP (Model Context Protocol) endpoints (bot users only)
	v1PrivateApis.HandleFunc("POST /interactions/{chat_uuid}/mcp", mcpHandler.HandleMCP)

	v1PrivateApis.HandleFunc("POST /contacts/add", contactsHandler.Add)
	v1PrivateApis.HandleFunc("GET  /contacts/list", contactsHandler.List)
	v1PrivateApis.HandleFunc("GET /contacts/{contact_token}", contactsHandler.GetContactByToken)

	v1PrivateApis.HandleFunc("GET /user/self", userHandler.Self)
	v1PrivateApis.HandleFunc("GET /user/permissions", userHandler.ListPermissions)
	v1PrivateApis.HandleFunc("POST /user/access-tokens", userHandler.CreateAccessToken)
	v1PrivateApis.HandleFunc("GET /user/access-tokens/list", userHandler.ListAccessTokens)
	v1PrivateApis.HandleFunc("POST /user/2fa/setup", userHandler.SetupTwoFactor)
	v1PrivateApis.HandleFunc("POST /user/2fa/confirm", userHandler.ConfirmTwoFactor)
	v1PrivateApis.HandleFunc("POST /user/2fa/disable", userHandler.DisableTwoFactor)
	v1PrivateApis.HandleFunc("POST /user/2fa/recovery-codes", userHandler.GenerateNewRecoveryCodes)
	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}", admin.GetTableInfo)
	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}/data", admin.GetTableDataPaginated)
	v1PrivateApis.HandleFunc("GET /admin/table/{table_name}/{id}", admin.GetTableItemById)
	v1PrivateApis.HandleFunc("DELETE /admin/table/{table_name}/{id}", admin.DeleteTableItemById)
	v1PrivateApis.HandleFunc("DELETE /admin/delete_all_entries/{table_name}", admin.DeleteAllEntries)
	v1PrivateApis.HandleFunc("GET /admin/tables", admin.GetAllTables)
	v1PrivateApis.HandleFunc("GET /admin/users", admin.GetUsersWithDetails)
	v1PrivateApis.HandleFunc("GET /admin/schema/sql", admin.GetSchemaSQL)
	v1PrivateApis.HandleFunc("GET /admin/docs/tag/{tag}", admin.GetCodeDocByTag)
	v1PrivateApis.HandleFunc("GET /admin/tests/go", admin.GetGoTestsOverview)
	v1PrivateApis.HandleFunc("GET /admin/server/config", admin.GetServerRuntimeConfig)
	v1PrivateApis.HandleFunc("GET /tools/rest", toolsHandler.ListDynamicRESTTools)
	v1PrivateApis.HandleFunc("POST /tools/rest/reload", toolsHandler.ReloadDynamicRESTTools)
	v1PrivateApis.HandleFunc("PUT /tools/rest/{tool_name}", toolsHandler.UpsertDynamicRESTTool)
	v1PrivateApis.HandleFunc("DELETE /tools/rest/{tool_name}", toolsHandler.DeleteDynamicRESTTool)
	v1PrivateApis.HandleFunc("GET /admin/docs/snapshots/{snapshot}/stats", admin.GetDocsSnapshotStatsByTag)
	v1PrivateApis.HandleFunc("POST /admin/docs/snapshots/{snapshot}/refresh", admin.RefreshDocsSnapshotByTag)
	v1PrivateApis.HandleFunc("GET /admin/docs/snapshots/{snapshot}/data", admin.GetDocsSnapshotDataByTag)
	v1PrivateApis.HandleFunc("POST /admin/docs/snapshots/write", admin.WriteDocsSnapshot)
	v1PrivateApis.HandleFunc("GET /admin/asynq/queues/{queue}/tasks", admin.ListAsynqTasks)
	v1PrivateApis.HandleFunc("GET /admin/asynq/queues/{queue}/tasks/{task_id}", admin.GetAsynqTask)
	v1PrivateApis.HandleFunc("GET /admin/asynq/queues/{queue}/stats", admin.GetAsynqQueueStats)
	v1PrivateApis.HandleFunc("POST /admin/bots/{bot_uuid}/models/selection", admin.UpdateBotModelSelection)

	v1PrivateApis.HandleFunc("GET /metrics", metricsHandler.Metrics)

	v1PrivateApis.HandleFunc("POST /files/upload", filesHandler.UploadFile)
	v1PrivateApis.HandleFunc("GET /files/{file_id}", filesHandler.GetFile)
	v1PrivateApis.HandleFunc("GET /files/{file_id}/info", filesHandler.GetFileInfo)
	v1PrivateApis.HandleFunc("GET /files/{file_id}/data", filesHandler.GetFileData)
	v1PrivateApis.HandleFunc("DELETE /files/{file_id}", filesHandler.DeleteFile)

	commonMiddlewares := CreateStack(
		dbMiddleware(DB),
		websocketMiddleware(websocketHandler),
		queueMiddleware(queueClient, queueInspector),
	)

	websocketMux.HandleFunc("/connect", websocketHandler.Connect)
	mux.Handle("GET /ws/interaction/{chat_share_uuid}", commonMiddlewares(Logging(http.HandlerFunc(websocketHandler.ConnectSharedInteraction))))
	mux.Handle("/ws/", http.StripPrefix("/ws", commonMiddlewares(AuthMiddleware(websocketMux))))
	mux.Handle("POST /api/v1/user/login", commonMiddlewares(http.HandlerFunc(userHandler.Login)))
	mux.Handle("POST /api/v1/user/logout", commonMiddlewares(http.HandlerFunc(userHandler.Logout)))
	mux.Handle("/admin/asynq/ui", commonMiddlewares(AuthMiddleware(http.HandlerFunc(admin.AsynqUIHandler(asynqUIHandler)))))
	mux.Handle("/admin/asynq/ui/", commonMiddlewares(AuthMiddleware(http.HandlerFunc(admin.AsynqUIHandler(asynqUIHandler)))))

	mux.Handle("POST /api/v1/user/register", commonMiddlewares(http.HandlerFunc(userHandler.Register)))
	mux.Handle("GET /api/tests/go", commonMiddlewares(Logging(http.HandlerFunc(admin.GetGoTestsOverview))))
	mux.Handle("GET /api/v1/models/list", commonMiddlewares(Logging(OptionalAuthMiddleware(http.HandlerFunc(modelsHandler.List)))))
	mux.Handle("GET /api/v1/tools/list", commonMiddlewares(Logging(OptionalAuthMiddleware(http.HandlerFunc(toolsHandler.List)))))
	mux.Handle("GET /api/v1/tools/typing", commonMiddlewares(Logging(OptionalAuthMiddleware(http.HandlerFunc(toolsHandler.ListTyping)))))
	mux.Handle("GET /api/v1/tools/{tool_name}/typing", commonMiddlewares(Logging(OptionalAuthMiddleware(http.HandlerFunc(toolsHandler.GetTyping)))))
	mux.Handle("POST /api/v1/tools/typing/{tool_name}/call/validate", commonMiddlewares(Logging(OptionalAuthMiddleware(http.HandlerFunc(toolsHandler.ValidateCallPayload)))))
	mux.Handle("POST /api/v1/tools/typing/{tool_name}/init/validate", commonMiddlewares(Logging(OptionalAuthMiddleware(http.HandlerFunc(toolsHandler.ValidateInitPayload)))))
	mux.Handle("POST /api/chat/{chat_uuid}/publish", commonMiddlewares(Logging(AuthMiddleware(http.HandlerFunc(chatsHandler.Publish)))))
	mux.Handle("POST /api/chat/{chat_uuid}/unpublish", commonMiddlewares(Logging(AuthMiddleware(http.HandlerFunc(chatsHandler.Unpublish)))))
	mux.Handle("GET /api/interaction/{chat_share_uuid}", commonMiddlewares(Logging(http.HandlerFunc(chatsHandler.GetSharedInteraction))))
	mux.Handle("GET /api/interaction/{chat_share_uuid}/messages", commonMiddlewares(Logging(http.HandlerFunc(chatsHandler.ListSharedInteractionMessages))))

	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", commonMiddlewares(Logging(AuthMiddleware(v1PrivateApis)))))

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
			mux.Handle(route, commonMiddlewares(FrontendAuthMiddleware(http.HandlerFunc(ServeFrontendRoute(route, "/index.html")))))
		}
		mux.Handle("/", commonMiddlewares(FrontendAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				ServeFrontendRoute("/", "index.html")(w, r)
			} else if tryServeFrontendPageContext(routes, w, r) {
				return
			} else if tryServeFrontendHTMLRoute(routes, w, r) {
				return
			} else {
				ServeFrontendRoute("/404", ".html")(w, r)
			}
		}))))
	} else {
		proxies := []FrontendProxy{{
			Name:     "frontend",
			Target:   frontendProxy,
			Prefixes: []string{"/"},
		}}

		if err := registerFrontendProxies(mux, proxies, commonMiddlewares); err != nil {
			log.Fatal(err)
		}
	}

	return mux, websocketHandler
}
