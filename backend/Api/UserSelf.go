package Api

import (
	"fmt"
	"net/http"
)

func UserSelfHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)

	w.Header().Set("Content-Type", "text/plain")

	_, err := w.Write([]byte("Hello, World!"))

	if err != nil {
		fmt.Println("Error writing response:", err)
	}
}
