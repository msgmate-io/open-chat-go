package server

import (
	"backend/database"
	"fmt"
	"net/http"

	"github.com/rs/cors"
)

func BackendServer(
	host string,
	port int64,
	debug bool,
	ssl bool,
) (*http.Server, string) {
	var protocol string
	var fullHost string

	router := BackendRouting()
	if ssl {
		protocol = "https"
	} else {
		protocol = "http"
	}

	fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

	server := &http.Server{
		Addr: fmt.Sprintf("%s:%d", host, port),
		Handler: CreateStack(
			database.SessionManager.LoadAndSave,
			// JsonBody, TODO: depricate bad practice
			Logging,
			cors.New(cors.Options{
				AllowedOrigins:   []string{"foo.com"},
				AllowCredentials: true,
				Debug:            debug,
			}).Handler,
		)(router),
	}

	return server, fullHost
}
