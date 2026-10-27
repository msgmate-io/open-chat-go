package server

import (
	"net/http"

	"backend/apierror"
)

// APIErrorResponse is the JSON envelope every API-level rejection uses. The
// middleware in this package guards *every* /api/v1 route, so a rejection coming
// from an authentication/authorization check must look exactly like a rejection
// coming from a handler: a JSON body with a machine readable code. Generated
// clients (and the live-runtime canary) call response.json() unconditionally, so
// a plain-text body makes an expected 4xx look like a client bug
// (open-chat-go-ci#865).
//
// APIErrorResponse is an alias of apierror.Response so that middleware, handler
// and (leaf-package) bot-endpoint rejections all emit byte-identical bodies.
type APIErrorResponse = apierror.Response

// writeAPIError writes a JSON error response with a stable machine readable
// code. It replaces http.Error on the middleware reject paths.
func writeAPIError(w http.ResponseWriter, status int, code string, message string) {
	apierror.Write(w, status, code, message)
}
