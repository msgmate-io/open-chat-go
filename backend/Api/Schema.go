package Api

import (
	"net/http"
	"os"
)

func SchemaHandler(w http.ResponseWriter, r *http.Request) {
	jsonData, err := os.ReadFile("./docs/swagger.json")
	if err != nil {
		http.Error(w, "Could not read file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}
