package server

import (
	"fmt"
	"github.com/rs/cors"
	"net/http"
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
			JsonBody,
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
