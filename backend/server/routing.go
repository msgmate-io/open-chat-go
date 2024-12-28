package server

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/federation"
	"backend/api/reference"
	"backend/api/user"
	"backend/api/websocket"
	"fmt"
	"net/http"
)

func BackendRouting(
	debug bool,
) *http.ServeMux {
	mux := http.NewServeMux()
	v1PrivateApis := http.NewServeMux()
	websocketMux := http.NewServeMux()

	userHandler := &user.UserHandler{}
	chatsHandler := &chats.ChatsHandler{}
	contactsHandler := &contacts.ContactsHander{}
	federationHandler := &federation.FederationHandler{}
	websocketHandler := websocket.ConnectionHandler

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
	v1PrivateApis.HandleFunc("POST /federation/nodes/{node_uuid}/request", federationHandler.RequestNode)

	mux.HandleFunc("POST /api/v1/user/login", userHandler.Login)
	mux.HandleFunc("POST /api/v1/user/register", userHandler.Register)
	mux.HandleFunc("GET /_health", func(w http.ResponseWriter, r *http.Request) {
		if ServerStatus != "running" {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(fmt.Sprintf("Server is not running, status: %s", ServerStatus)))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Server is running"))
		}
	})
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", Logging(AuthMiddleware(v1PrivateApis))))
	mux.HandleFunc("/reference", reference.ScalarReference)

	websocketMux.HandleFunc("/connect", websocketHandler.Connect)
	mux.Handle("/ws/", http.StripPrefix("/ws", AuthMiddleware(websocketMux)))

	return mux
}
