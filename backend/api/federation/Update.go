package federation

import (
	"backend/database"
	"backend/server/util"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// wget --header="Cookie: session_id=YOUR_SESSION_ID" http://localhost:1984/api/v1/download/bin
func (h *FederationHandler) DownloadBinary(w http.ResponseWriter, r *http.Request) {
	_, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
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

type RequestSelfUpdate struct {
	BinaryOwnerPeerId string `json:"binary_owner_peer_id"`
	NetworkName       string `json:"network_name"`
}

func (h *FederationHandler) RequestSelfUpdate(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var request RequestSelfUpdate
	err = json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Unable to decode request", http.StatusBadRequest)
		return
	}

	var node database.Node
	err = DB.Where("peer_id = ?", request.BinaryOwnerPeerId).Preload("Addresses").First(&node).Error
	if err != nil {
		http.Error(w, "Unable to find node", http.StatusBadRequest)
		return
	}

	resp, err := SendRequestToNode(DB, h, node, RequestNode{
		Method:  "GET",
		Path:    fmt.Sprintf("/api/v1/bin/download"),
		Headers: map[string]string{},
		Body:    "",
	}, T1mNetworkRequestProtocolID)

	if err != nil {
		http.Error(w, "Failed to send request to node", http.StatusInternalServerError)
		return
	}

	if resp == nil {
		http.Error(w, "Received nil response from node", http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()

	randomBinaryName := fmt.Sprintf("%d.bin", time.Now().UnixNano())
	binaryPath := fmt.Sprintf("/tmp/%s", randomBinaryName)
	binary, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Unable to save binary", http.StatusInternalServerError)
		return
	}
	err = os.WriteFile(binaryPath, binary, 0644)
	if err != nil {
		http.Error(w, "Unable to save binary", http.StatusInternalServerError)
		return
	}

	fmt.Println("Binary path:", binaryPath)

	// Make the binary executable
	err = os.Chmod(binaryPath, 0755)
	if err != nil {
		http.Error(w, "Unable to make binary executable", http.StatusInternalServerError)
		return
	}

	// Updating requires some trickery cause we have to kill the own process first
	// so what we do is we point the service to `backend_updated` and then restart it.
	// The service itself detechts if it's called `backend_updated` and the copies the backend update again to `backend` then restarts the service

	// copy file to `/usr/local/bin/backend_updated`
	err = os.Rename(binaryPath, "/usr/local/bin/backend_updated")
	if err != nil {
		http.Error(w, "Unable to copy binary to /usr/local/bin/backend_updated", http.StatusInternalServerError)
		return
	}

	// edit the service file to point to `/usr/local/bin/backend_updated`
	serviceFilePath := "/etc/systemd/system/open-chat.service"
	serviceFile, err := os.ReadFile(serviceFilePath)
	if err != nil {
		http.Error(w, "Unable to read service file", http.StatusInternalServerError)
		return
	}

	// replace the line `ExecStart=/usr/local/bin/backend` with `ExecStart=/usr/local/bin/backend_updated`
	serviceFile = bytes.ReplaceAll(serviceFile, []byte("ExecStart=/usr/local/bin/backend"), []byte("ExecStart=/usr/local/bin/backend_updated"))
	err = os.WriteFile(serviceFilePath, serviceFile, 0644)
	if err != nil {
		http.Error(w, "Unable to write service file", http.StatusInternalServerError)
		return
	}

	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		http.Error(w, "Unable to reload systemd", http.StatusInternalServerError)
		return
	}

	// Now reload the service and restart the service
	cmd := exec.Command("systemctl", "restart", "open-chat")
	cmd.Run()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Installation started successfully"))
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

func (h *FederationHandler) GetHiveSetupScript(w http.ResponseWriter, r *http.Request) {
	// get query param ?key=
	key := r.URL.Query().Get("key")

	// TODO improve!
	if key != "ShowMe" {
		http.Error(w, "Invalid key", http.StatusForbidden)
		return
	}

	script := `#!/bin/bash
SERVER_HOST="http://89.58.25.188:39670"
wget $SERVER_HOST/api/v1/bin/download -O backend
sudo chmod +x backend
sudo ./backend install -p 39670 --host 0.0.0.0 -pp2p 39672 --dnc hive:4FmM0QDokhnLATe8DR -rc 'controller:hashed_$2a$10$p7X/uGcPT8phfqHaNgJIRuzPsCXyuh.lDEpX1PdolXbgK6s/VkaWy' -bs eyJuYW1lIjoiYm9vdHN0cmFwcGVyXzg5XzU4XzI1XzE4OCIsImFkZHJlc3NlcyI6WyIvaXA0Lzg5LjU4LjI1LjE4OC90Y3AvMzk2NzIvcDJwL1FtVVFFOGN1NXpyTkNXZDlScXp6VkFyaUNyZHlITXFGREFVMkZoaDhUNExCZngiXX0K
# wget http://89.58.25.188:39670/api/v1/bin/setup?key=ShowMe -O - | bash`

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}
