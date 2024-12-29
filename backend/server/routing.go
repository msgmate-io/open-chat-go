package server

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/federation"
	"backend/api/reference"
	"backend/api/tls"
	"backend/api/user"
	"backend/api/websocket"
	"context"
	"embed"
	"fmt"
	"gorm.io/gorm"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

//go:embed all:frontend
var frontendFS embed.FS

func ProxyRequestHandler(proxy *httputil.ReverseProxy, url *url.URL, endpoint string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[ TinyRP ] Request received at %s at %s\n", r.URL, time.Now().UTC())
		// Update the headers to allow for SSL redirection
		r.URL.Host = url.Host
		r.URL.Scheme = url.Scheme
		r.Header.Set("X-Forwarded-Host", r.Header.Get("Host"))
		r.Host = url.Host
		//trim reverseProxyRoutePrefix
		path := r.URL.Path
		r.URL.Path = strings.TrimLeft(path, endpoint)
		// Note that ServeHttp is non blocking and uses a go routine under the hood
		fmt.Printf("[ TinyRP ] Redirecting request to %s at %s\n", r.URL, time.Now().UTC())
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

func BackendRouting(
	DB *gorm.DB,
	federationHandler *federation.FederationHandler,
	debug bool,
	frontendProxy string,
) (*http.ServeMux, *websocket.WebSocketHandler) {
	mux := http.NewServeMux()
	v1PrivateApis := http.NewServeMux()
	websocketMux := http.NewServeMux()

	userHandler := &user.UserHandler{}
	chatsHandler := &chats.ChatsHandler{}
	contactsHandler := &contacts.ContactsHander{}
	websocketHandler := websocket.NewWebSocketHandler()

	v1PrivateApis.HandleFunc("GET /chats/list", chatsHandler.List)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/messages/list", chatsHandler.ListMessages)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}", chatsHandler.GetChat)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/messages/send", chatsHandler.MessageSend)
	v1PrivateApis.HandleFunc("POST /chats/create", chatsHandler.Create)

	v1PrivateApis.HandleFunc("POST /contacts/add", contactsHandler.Add)
	v1PrivateApis.HandleFunc("GET  /contacts/list", contactsHandler.List)

	v1PrivateApis.HandleFunc("GET /user/self", userHandler.Self)
	v1PrivateApis.HandleFunc("GET /federation/identity", federationHandler.Identity)
	v1PrivateApis.HandleFunc("POST /federation/nodes/register", federationHandler.RegisterNode)
	v1PrivateApis.HandleFunc("GET /federation/nodes/list", federationHandler.ListNodes)
	v1PrivateApis.HandleFunc("GET /federation/nodes/whitelisted", federationHandler.WhitelistedPeers)
	v1PrivateApis.HandleFunc("POST /federation/nodes/{node_uuid}/request", federationHandler.RequestNode)
	v1PrivateApis.HandleFunc("POST /federation/nodes/peer/{peer_id}/request", federationHandler.RequestNodeByPeerId)
	v1PrivateApis.HandleFunc("POST /federation/nodes/proxy", federationHandler.CreateAndStartProxy)
	v1PrivateApis.HandleFunc("GET /tls/keys", tls.ListKeys)
	v1PrivateApis.HandleFunc("POST /tls/acme/solve", tls.SolveACMEChallengeHandler)

	providerMiddlewares := CreateStack(
		dbMiddleware(DB),
		websocketMiddleware(websocketHandler),
	)

	mux.Handle("POST /api/v1/user/login", providerMiddlewares(http.HandlerFunc(userHandler.Login)))
	mux.Handle("POST /api/v1/user/register", providerMiddlewares(http.HandlerFunc(userHandler.Register)))

	mux.Handle("POST /api/v1/federation/networks/login", providerMiddlewares(http.HandlerFunc(userHandler.NetworkUserLogin)))
	v1PrivateApis.HandleFunc("POST /federation/networks/addnode", federationHandler.RegisterNode)
	v1PrivateApis.HandleFunc("GET /federation/networks/sync/{network_name}/get", federationHandler.SyncGet)

	mux.Handle("POST /api/v1/federation/nodes/{peer_id}/ping", providerMiddlewares(http.HandlerFunc(federationHandler.Ping)))

	// TODO deprivate again? Now basicly handeled trough networks
	mux.Handle("POST /api/v1/federation/nodes/register/request", providerMiddlewares(http.HandlerFunc(federationHandler.RequestNodeRegistration)))

	mux.HandleFunc("GET /_health", func(w http.ResponseWriter, r *http.Request) {
		// TODO: need some better way to check if server is running
		if ServerStatus != "running" {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(fmt.Sprintf("Server is not running, status: %s", ServerStatus)))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Server is running"))
		}
	})
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", providerMiddlewares(Logging(AuthMiddleware(v1PrivateApis)))))
	mux.HandleFunc("/reference", reference.ScalarReference)

	websocketMux.HandleFunc("/connect", websocketHandler.Connect)
	mux.Handle("/ws/", http.StripPrefix("/ws", providerMiddlewares(AuthMiddleware(websocketMux))))

	// server frontend or proxy to dev-frontend
	if frontendProxy == "" {
		fsys, err := fs.Sub(frontendFS, "frontend")
		if err != nil {
			log.Fatal(err)
		}
		fs := http.FileServer(http.FS(fsys))
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Serve static assets directly
			if strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/build/") {
				if _, err := fsys.Open(strings.TrimPrefix(path, "/")); err != nil {
					http.NotFound(w, r)
					return
				}
				fs.ServeHTTP(w, r)
				return
			}

			// For all other routes, serve index.html directly from the filesystem
			indexFile, err := fsys.Open("index.html")
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			defer indexFile.Close()

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, "index.html", time.Time{}, indexFile.(io.ReadSeeker))
		}))
	} else {
		targetURL, err := url.Parse(frontendProxy)
		if err != nil {
			log.Fatal(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		mux.HandleFunc("/", ProxyRequestHandler(proxy, targetURL, "/"))
	}

	return mux, websocketHandler
}
