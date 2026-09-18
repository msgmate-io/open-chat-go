package bots

import (
	"net/http"

	"backend/apierror"
)

// writeBotAPIError writes a JSON error body for the bot endpoints. Generated
// clients (and the live-runtime canary) call response.json() on the response
// regardless of the status code, so a plain-text http.Error makes an expected
// rejection look like a client-side JSONDecodeError and hides the real cause
// (open-chat-go-ci#865).
func writeBotAPIError(w http.ResponseWriter, status int, code string, message string) {
	apierror.Write(w, status, code, message)
}

// writeBotAPIDetailedError writes the same JSON envelope as writeBotAPIError
// but additionally carries the underlying server-side cause in the detail
// field. writeCreateInteractionError uses it for the generic 500 so the next
// Live Runtime Canary run reports the real create-interaction failure instead
// of an empty body (open-chat-go-ci#865).
func writeBotAPIDetailedError(w http.ResponseWriter, status int, code string, message string, detail string) {
	if detail == "" {
		writeBotAPIError(w, status, code, message)
		return
	}
	apierror.WriteDetailed(w, status, code, message, detail)
}
