//go:build federation
// +build federation

package server

import (
	"backend/api/federation"
	"net/http"

	"gorm.io/gorm"
)

// getDomainRoutingMiddleware returns the federation domain routing middleware
func getDomainRoutingMiddleware(DB *gorm.DB, cookieDomain string) func(http.Handler) http.Handler {
	return federation.DomainRoutingMiddleware(DB, cookieDomain)
}
