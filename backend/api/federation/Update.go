package federation

import (
	"backend/server/util"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// wget --header="Cookie: session_id=YOUR_SESSION_ID" http://localhost:1984/api/v1/download/bin
func (h *FederationHandler) DownloadBinary(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Check if the user is an admin
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	// Path to the binary file
	binaryPath := os.Args[0]
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		http.Error(w, "Binary file not found", http.StatusNotFound)
		return
	}

	// Serve the binary file
	w.Header().Set("Content-Disposition", "attachment; filename=binary_name")
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, binaryPath)
}

func (h *FederationHandler) UploadBinary(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Check if the user is an admin
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	// check if the ?install=true is in the request
	install := r.URL.Query().Get("install") == "true"

	// Parse the multipart form
	err = r.ParseMultipartForm(10 << 20) // Limit your max memory usage
	if err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}

	// Retrieve the file from form data
	file, _, err := r.FormFile("binary")
	if err != nil {
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	randomFileName := fmt.Sprintf("%d.bin", time.Now().UnixNano())

	// Create a file on the server
	dst, err := os.Create(randomFileName)
	if err != nil {
		http.Error(w, "Unable to create file on server", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the uploaded file to the server
	_, err = io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	if install {
		// install the binary by running `./binary_name install`
		cmd := exec.Command("./"+randomFileName, "install")
		cmd.Run()
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("File uploaded successfully"))
}
