//go:build !federation
// +build !federation

package server

import (
	"net/http"

	"gorm.io/gorm"
)

// getDomainRoutingMiddleware returns a no-op middleware when federation is disabled
func getDomainRoutingMiddleware(DB *gorm.DB, cookieDomain string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}
