package server

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/api/reference"
	"backend/api/user"
	"backend/api/websocket"
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
	websocketHandler := websocket.ConnectionHandler

	v1PrivateApis.HandleFunc("GET /chats/list", chatsHandler.List)
	v1PrivateApis.HandleFunc("GET /chats/{chat_uuid}/messages/list", chatsHandler.ListMessages)
	v1PrivateApis.HandleFunc("POST /chats/{chat_uuid}/messages/send", chatsHandler.MessageSend)
	v1PrivateApis.HandleFunc("POST /chats/create", chatsHandler.Create)

	v1PrivateApis.HandleFunc("POST /contacts/add", contactsHandler.Add)
	v1PrivateApis.HandleFunc("GET  /contacts/list", contactsHandler.List)

	v1PrivateApis.HandleFunc("GET /user/self", userHandler.Self)

	mux.HandleFunc("POST /api/v1/user/login", userHandler.Login)
	mux.HandleFunc("POST /api/v1/user/register", userHandler.Register)
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", Logging(AuthMiddleware(v1PrivateApis))))
	mux.HandleFunc("/reference", reference.ScalarReference)

	websocketMux.HandleFunc("/connect", websocketHandler.Connect)
	mux.Handle("/ws/", http.StripPrefix("/ws", AuthMiddleware(websocketMux)))

	return mux
}
