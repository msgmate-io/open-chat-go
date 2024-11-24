package server

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"log"
	"net/http"
)

func GenerateToken(email string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(email), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Hash to store:", string(hash))

	hasher := md5.New()
	hasher.Write(hash)
	return hex.EncodeToString(hasher.Sum(nil))
}

func BackendServer(
	host string,
	port int64,
	debug bool,
	ssl bool,
) (*http.Server, string) {
	var protocol string
	var fullHost string

	router := BackendRouting(debug)
	if ssl {
		protocol = "https"
	} else {
		protocol = "http"
	}

	fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: router,
	}

	return server, fullHost
}
