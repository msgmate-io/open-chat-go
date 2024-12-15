package server

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/federation"
	"backend/api/reference"
	"backend/api/user"
	"backend/api/websocket"
	"context"
	"fmt"
	"gorm.io/gorm"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

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
	v1PrivateApis.HandleFunc("POST /federation/nodes/{node_uuid}/request", federationHandler.RequestNode)
	v1PrivateApis.HandleFunc("POST /federation/nodes/proxy/{node_uuid}/{local_port}/", federationHandler.CreateAndStartProxy)

	providerMiddlewares := CreateStack(
		dbMiddleware(DB),
		websocketMiddleware(websocketHandler),
	)

	mux.Handle("POST /api/v1/user/login", providerMiddlewares(http.HandlerFunc(userHandler.Login)))
	mux.Handle("POST /api/v1/user/register", providerMiddlewares(http.HandlerFunc(userHandler.Register)))
	mux.Handle("POST /api/v1/federation/nodes/{peer_id}/ping", providerMiddlewares(http.HandlerFunc(federationHandler.Ping)))
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
	fs := http.FileServer(http.Dir("./frontend"))
	if frontendProxy == "" {
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				if _, err := http.Dir("./frontend").Open(r.URL.Path); err != nil {
					http.ServeFile(w, r, "./frontend/index.html")
					return
				}
			}
			fs.ServeHTTP(w, r)
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
